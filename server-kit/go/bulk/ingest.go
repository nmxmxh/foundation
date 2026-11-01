package bulk

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"

	apperrors "github.com/nmxmxh/ovasabi_foundation/server-kit/go/errors"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/kernellane"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/objectstore"
)

// DurableObjectStore is an optional ObjectStore capability: SyncObject returns only
// after a stored object survives a crash. Source reclamation requires it, because it
// destroys the only other copy of the bytes.
type DurableObjectStore interface {
	ObjectStore
	SyncObject(ctx context.Context, key string) error
}

// IngestFileOptions controls IngestFile.
type IngestFileOptions struct {
	// ReclaimSource frees the source file prefix after each part is durable.
	//
	// WARNING: This deletes the caller's source bytes and cannot be undone. A part that
	// is freed can be served only from the object store. Use it for spool or staging
	// files only. It needs CheckpointPath and a DurableObjectStore.
	ReclaimSource bool
	// CheckpointPath records the freed prefix so that a restart can detect lost receipts.
	CheckpointPath string
}

// IngestFileResult reports the parts of an IngestFile call.
type IngestFileResult struct {
	Receipts []PartReceipt
	// ReclaimedOffset is the source prefix the checkpoint records as released, or 0 without
	// ReclaimSource. Where Punched is false, those bytes are released but still on disk.
	ReclaimedOffset int64
	// Punched reports whether the kernel freed blocks. It is false where hole punching
	// is not supported; the source then keeps its bytes and ingest still succeeds.
	Punched bool
}

// IngestFile stores a same-host file as the parts of an initiated transfer.
//
// Parts that already have a receipt are not read again, so a restart resumes at the
// first missing part. With ReclaimSource, each part is stored, synced, and recorded
// in the checkpoint before its source blocks are freed. Each part is an independent
// frame, so the part boundary is a safe restart point for every encoding.
// IngestFile does not call Complete.
func (m *Manager) IngestFile(ctx context.Context, transferID string, src *os.File, opts IngestFileOptions) (IngestFileResult, error) {
	plan, err := m.loadPlanForMutation(ctx, transferID, "bulk:file:ingest:v1:failed")
	if err != nil {
		return IngestFileResult{}, err
	}
	if err := m.validateIngest(plan, src, opts); err != nil {
		m.emitFailure(ctx, "bulk:file:ingest:v1:failed", plan.TransferID, err)
		return IngestFileResult{}, err
	}
	if m.events != nil {
		if err := m.emit(ctx, "bulk:file:ingest:v1:requested", planEventPayload(plan)); err != nil {
			return IngestFileResult{}, err
		}
	}
	ing := &fileIngest{m: m, plan: plan, src: src, opts: opts}
	if opts.ReclaimSource {
		if err := ing.openReclaimer(); err != nil {
			m.emitFailure(ctx, "bulk:file:ingest:v1:failed", plan.TransferID, err)
			return IngestFileResult{}, err
		}
		defer func() { _ = ing.reclaimer.Close() }()
	}
	count := expectedPartCount(plan)
	for part := range count {
		if err := ctx.Err(); err != nil {
			return ing.result, err
		}
		if err := ing.ingestPart(ctx, part); err != nil {
			m.emitFailure(ctx, "bulk:file:ingest:v1:failed", plan.TransferID, err)
			return ing.result, err
		}
	}
	if m.events != nil {
		if err := m.emit(ctx, "bulk:file:ingest:v1:success", planEventPayload(plan)); err != nil {
			return ing.result, err
		}
	}
	return ing.result, nil
}

func (m *Manager) validateIngest(plan TransferPlan, src *os.File, opts IngestFileOptions) error {
	if src == nil {
		return apperrors.New(apperrors.CodeValidation, "source file is required")
	}
	st, err := src.Stat()
	if err != nil {
		return storePartError(err, "stat source file")
	}
	if st.Size() != plan.TotalSize {
		return apperrors.New(apperrors.CodeValidation, "source file size does not match transfer total_size")
	}
	if !opts.ReclaimSource {
		return nil
	}
	if strings.TrimSpace(opts.CheckpointPath) == "" {
		return apperrors.New(apperrors.CodeValidation, "checkpoint_path is required to reclaim the source")
	}
	if _, ok := m.objects.(DurableObjectStore); !ok {
		return apperrors.New(apperrors.CodeInvalidState, "object store cannot confirm durability; source reclamation refused")
	}
	return nil
}

// fileIngest holds the state of one IngestFile call.
type fileIngest struct {
	m         *Manager
	plan      TransferPlan
	src       *os.File
	opts      IngestFileOptions
	reclaimer *kernellane.Reclaimer
	// freedFloor is the prefix a previous run already freed. A part below it must have a receipt.
	freedFloor int64
	result     IngestFileResult
}

func (ing *fileIngest) openReclaimer() error {
	wm, err := kernellane.LoadCheckpoint(ing.opts.CheckpointPath)
	switch {
	case err == nil:
		if wm.Aborted {
			return apperrors.Wrap(kernellane.ErrReclaimerAborted, apperrors.CodeInvalidState, "source reclamation was aborted")
		}
		if wm.SourceTotalSize != ing.plan.TotalSize {
			return apperrors.New(apperrors.CodeConflict, "reclaim checkpoint belongs to a source of another size")
		}
		ing.freedFloor = wm.SourcePunchedOffset
	case !errors.Is(err, fs.ErrNotExist):
		return apperrors.Wrap(err, apperrors.CodeDependency, "load reclaim checkpoint")
	}
	r, err := kernellane.NewReclaimer(ing.src, kernellane.ReclaimOptions{CheckpointPath: ing.opts.CheckpointPath})
	if err != nil {
		return apperrors.Wrap(err, apperrors.CodeValidation, "open source reclaimer")
	}
	ing.reclaimer = r
	ing.result.ReclaimedOffset = ing.freedFloor
	return nil
}

func (ing *fileIngest) ingestPart(ctx context.Context, part int) error {
	offset := int64(part) * ing.plan.ChunkSize
	size := min64(ing.plan.ChunkSize, ing.plan.TotalSize-offset)
	receipt, err := ing.m.state.LoadReceipt(ctx, ing.plan.OrganizationID, ing.plan.TransferID, part)
	if err != nil {
		if offset+size <= ing.freedFloor {
			// The bytes are gone and the receipt is lost. Reading now would store zeros.
			return apperrors.Wrap(kernellane.ErrUnrecoverableSparseSource, apperrors.CodeInvalidState,
				"part was reclaimed but has no receipt")
		}
		receipt, err = ing.storePart(ctx, part, offset, size)
		if err != nil {
			return err
		}
	} else if receipt.RawSize != size {
		return apperrors.New(apperrors.CodeConflict, "existing part receipt does not match the source layout")
	}
	ing.result.Receipts = append(ing.result.Receipts, receipt)
	if ing.reclaimer == nil {
		return nil
	}
	return ing.reclaim(ctx, receipt, offset+size)
}

func (ing *fileIngest) storePart(ctx context.Context, part int, offset, size int64) (PartReceipt, error) {
	hr := newHashingReader(io.NewSectionReader(ing.src, offset, size))
	if _, err := io.Copy(io.Discard, hr); err != nil {
		return PartReceipt{}, storePartError(err, "hash source part")
	}
	desc := PartDescriptor{PartNumber: part, Offset: offset, Size: size, ExpectedRawSHA256: hr.SumHex()}
	return ing.m.AcceptPart(ctx, ing.plan.TransferID, desc, io.NewSectionReader(ing.src, offset, size))
}

// reclaim frees the source up to end. The order is the safety argument: the receipt is
// saved, then the object is synced, then Commit writes the checkpoint, then it punches.
func (ing *fileIngest) reclaim(ctx context.Context, receipt PartReceipt, end int64) error {
	durable, _ := ing.m.objects.(DurableObjectStore)
	if err := durable.SyncObject(ctx, receipt.ObjectKey); err != nil {
		if errors.Is(err, objectstore.ErrNotDurable) {
			// The part is stored and nothing is freed. The store refuses durability, not a fault.
			return apperrors.Wrap(err, apperrors.CodeInvalidState, "object store is not durable; source reclamation refused")
		}
		return apperrors.Wrap(err, apperrors.CodeDependency, "sync stored part before reclaiming source")
	}
	punched, err := ing.reclaimer.Commit(end, end, receipt.RawSHA256)
	if err != nil {
		return apperrors.Wrap(err, apperrors.CodeDependency, "reclaim source prefix")
	}
	ing.result.Punched = ing.result.Punched || punched
	ing.result.ReclaimedOffset = max(ing.result.ReclaimedOffset, ing.reclaimer.Watermark().SourcePunchedOffset)
	return nil
}
