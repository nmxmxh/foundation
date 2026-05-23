package bulk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	apperrors "github.com/nmxmxh/ovasabi_foundation/server-kit/go/errors"
)

func (m *Manager) Status(ctx context.Context, transferID string) (TransferStatus, error) {
	plan, err := m.loadPlanForRead(ctx, transferID)
	if err != nil {
		return TransferStatus{}, err
	}
	receipts, err := m.state.ListReceipts(ctx, plan.OrganizationID, plan.TransferID)
	if err != nil {
		return TransferStatus{}, apperrors.Wrap(err, apperrors.CodeDependency, "list part receipts")
	}
	status := transferStatus(plan, receipts, m.clock())
	manifest, err := m.state.LoadManifest(ctx, plan.OrganizationID, plan.TransferID)
	if err == nil {
		status.ManifestKey = manifest.ManifestKey
		status.RootSHA256 = manifest.RootSHA256
		status.State = StateCompleted
		return status, nil
	}
	if errors.Is(err, ErrNotFound) {
		return status, nil
	}
	return TransferStatus{}, apperrors.Wrap(err, apperrors.CodeDependency, "load transfer manifest")
}

func transferStatus(plan TransferPlan, receipts []PartReceipt, now time.Time) TransferStatus {
	accepted := make([]bool, expectedPartCount(plan))
	var bytesAccepted int64
	for _, receipt := range receipts {
		if receipt.PartNumber >= 0 && receipt.PartNumber < len(accepted) {
			accepted[receipt.PartNumber] = true
		}
		bytesAccepted += receipt.RawSize
	}
	missing := missingParts(plan, accepted)
	return TransferStatus{
		TransferID:     plan.TransferID,
		OrganizationID: plan.OrganizationID,
		CorrelationID:  plan.CorrelationID,
		State:          plan.State,
		TotalSize:      plan.TotalSize,
		ChunkSize:      plan.ChunkSize,
		BytesAccepted:  bytesAccepted,
		PartsAccepted:  len(receipts),
		MissingParts:   missing,
		ManifestKey:    plan.ManifestKey,
		ResumeToken:    resumeToken(plan, receipts, bytesAccepted),
		UpdatedAt:      now,
	}
}

func expectedPartCount(plan TransferPlan) int {
	if plan.TotalSize == 0 {
		return 0
	}
	count := ceilDiv(plan.TotalSize, plan.ChunkSize)
	if count > int64(plan.MaxParts) {
		return plan.MaxParts
	}
	return int(count)
}

func missingParts(plan TransferPlan, accepted []bool) []MissingPart {
	if len(accepted) == 0 {
		return nil
	}
	missing := make([]MissingPart, 0)
	for partNumber, ok := range accepted {
		if ok {
			continue
		}
		offset := int64(partNumber) * plan.ChunkSize
		size := min64(plan.ChunkSize, plan.TotalSize-offset)
		missing = append(missing, MissingPart{
			PartNumber: partNumber,
			Offset:     offset,
			Size:       size,
		})
	}
	return missing
}

func resumeToken(plan TransferPlan, receipts []PartReceipt, bytesAccepted int64) string {
	h := sha256.New()
	_, _ = h.Write([]byte(plan.OrganizationID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(plan.TransferID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(plan.IdempotencyKey))
	_, _ = h.Write([]byte{0})
	var buf [32]byte
	tmp := strconv.AppendInt(buf[:0], bytesAccepted, 10)
	_, _ = h.Write(tmp)
	for _, receipt := range receipts {
		_, _ = h.Write([]byte{0})
		tmp = strconv.AppendInt(buf[:0], int64(receipt.PartNumber), 10)
		_, _ = h.Write(tmp)
		_, _ = h.Write([]byte{':'})
		_, _ = h.Write([]byte(receipt.RawSHA256))
	}
	var encoded [sha256.Size * 2]byte
	sum := h.Sum(nil)
	hex.Encode(encoded[:], sum)
	return "bulk1_" + string(encoded[:])
}
