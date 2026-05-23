package bulk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"testing"
	"time"

	runtimeconfig "github.com/nmxmxh/ovasabi_foundation/config-contracts/go/runtimeconfig"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/objectstore"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

func BenchmarkManagerAcceptPartIdentity(b *testing.B) {
	for _, size := range []int{64 << 10, 1 << 20, 4 << 20} {
		b.Run(fmt.Sprintf("%dKB", size>>10), func(b *testing.B) {
			payload := bulkBenchmarkPayload(size)
			digest := sha256HexBytes(payload)
			ctx := benchmarkContext()
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				mgr := newBenchmarkManager(b, nil, nil)
				transferID := fmt.Sprintf("identity-%d", i)
				plan := benchmarkPlan(transferID, int64(size), int64(size), 1)
				if err := mgr.state.SavePlan(ctx, plan); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				if _, err := mgr.AcceptPart(ctx, plan.TransferID, PartDescriptor{
					PartNumber:        0,
					Size:              int64(size),
					ExpectedRawSHA256: digest,
				}, bytes.NewReader(payload)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkManagerAcceptPartWithCacheAndEvents(b *testing.B) {
	size := 256 << 10
	payload := bulkBenchmarkPayload(size)
	digest := sha256HexBytes(payload)
	ctx := benchmarkContext()
	b.SetBytes(int64(size))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		mgr := newBenchmarkManager(b, redis.NewMemoryClient("bench"), events.NewInMemoryBus(256))
		transferID := fmt.Sprintf("identity-cache-events-%d", i)
		plan := benchmarkPlan(transferID, int64(size), int64(size), 1)
		if err := mgr.state.SavePlan(ctx, plan); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if _, err := mgr.AcceptPart(ctx, plan.TransferID, PartDescriptor{
			PartNumber:        0,
			Size:              int64(size),
			ExpectedRawSHA256: digest,
		}, bytes.NewReader(payload)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkManagerAcceptPartDuplicateReplay(b *testing.B) {
	size := 256 << 10
	payload := bulkBenchmarkPayload(size)
	digest := sha256HexBytes(payload)
	ctx := benchmarkContext()
	mgr := newBenchmarkManager(b, redis.NewMemoryClient("bench"), events.NewInMemoryBus(256))
	plan := benchmarkPlan("duplicate-replay", int64(size), int64(size), 1)
	if err := mgr.state.SavePlan(ctx, plan); err != nil {
		b.Fatal(err)
	}
	receipt, err := mgr.AcceptPart(ctx, plan.TransferID, PartDescriptor{
		PartNumber:        0,
		Size:              int64(size),
		ExpectedRawSHA256: digest,
	}, bytes.NewReader(payload))
	if err != nil {
		b.Fatal(err)
	}

	desc := PartDescriptor{PartNumber: receipt.PartNumber, Size: receipt.RawSize, ExpectedRawSHA256: receipt.RawSHA256}
	b.SetBytes(int64(size))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		replayed, err := mgr.AcceptPart(ctx, plan.TransferID, desc, errReader{})
		if err != nil {
			b.Fatal(err)
		}
		if !replayed.IdempotentReplay {
			b.Fatal("expected idempotent replay")
		}
	}
}

func BenchmarkManagerAcceptPartGzipCompressible(b *testing.B) {
	benchmarkManagerAcceptPartCompression(b, EncodingGzip, bulkCompressiblePayload(1<<20))
}

func BenchmarkManagerAcceptPartZstdCompressible(b *testing.B) {
	benchmarkManagerAcceptPartCompression(b, EncodingZstd, bulkCompressiblePayload(1<<20))
}

func BenchmarkManagerAcceptPartBrotliCompressible(b *testing.B) {
	benchmarkManagerAcceptPartCompression(b, EncodingBrotli, bulkCompressiblePayload(1<<20))
}

func BenchmarkManagerAcceptPartAutoCompressible(b *testing.B) {
	benchmarkManagerAcceptPartCompression(b, EncodingAuto, bulkCompressiblePayload(1<<20))
}

func BenchmarkManagerAcceptPartAutoIncompressible(b *testing.B) {
	benchmarkManagerAcceptPartCompression(b, EncodingAuto, bulkBenchmarkPayload(1<<20))
}

func benchmarkManagerAcceptPartCompression(b *testing.B, encoding string, payload []byte) {
	b.Helper()
	size := 1 << 20
	digest := sha256HexBytes(payload)
	ctx := benchmarkContext()
	b.SetBytes(int64(size))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		mgr := newBenchmarkManager(b, nil, nil)
		transferID := fmt.Sprintf("%s-%d", encoding, i)
		plan := benchmarkPlan(transferID, int64(size), int64(size), 1)
		plan.Compression = encoding
		if err := mgr.state.SavePlan(ctx, plan); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if _, err := mgr.AcceptPart(ctx, plan.TransferID, PartDescriptor{
			PartNumber:        0,
			Size:              int64(size),
			ExpectedRawSHA256: digest,
		}, bytes.NewReader(payload)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkManagerCompleteManifest(b *testing.B) {
	for _, parts := range []int{128, 1024} {
		b.Run(fmt.Sprintf("%dParts", parts), func(b *testing.B) {
			ctx := benchmarkContext()
			partSize := int64(64 << 10)
			totalSize := int64(parts) * partSize
			b.SetBytes(totalSize)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				mgr := newBenchmarkManager(b, nil, nil)
				transferID := fmt.Sprintf("complete-%d-%d", parts, i)
				plan := benchmarkPlan(transferID, totalSize, partSize, parts)
				if err := mgr.state.SavePlan(ctx, plan); err != nil {
					b.Fatal(err)
				}
				receipts := benchmarkReceipts(plan, parts, partSize)
				for _, receipt := range receipts {
					if err := mgr.state.SaveReceipt(ctx, receipt); err != nil {
						b.Fatal(err)
					}
				}
				b.StartTimer()
				if _, err := mgr.Complete(ctx, transferID, CompleteRequest{ExpectedRootSHA256: manifestRoot(receipts)}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkManagerCompleteManifestSparseMissing(b *testing.B) {
	ctx := benchmarkContext()
	partSize := int64(64 << 10)
	parts := 1024
	totalSize := int64(parts) * partSize
	b.SetBytes(totalSize)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		mgr := newBenchmarkManager(b, nil, nil)
		transferID := fmt.Sprintf("sparse-complete-%d", i)
		plan := benchmarkPlan(transferID, totalSize, partSize, parts)
		if err := mgr.state.SavePlan(ctx, plan); err != nil {
			b.Fatal(err)
		}
		receipts := benchmarkReceipts(plan, parts, partSize)
		for index, receipt := range receipts {
			if index == parts/2 {
				continue
			}
			if err := mgr.state.SaveReceipt(ctx, receipt); err != nil {
				b.Fatal(err)
			}
		}
		b.StartTimer()
		if _, err := mgr.Complete(ctx, transferID, CompleteRequest{}); err == nil {
			b.Fatal("expected sparse manifest completion to fail")
		}
	}
}

func BenchmarkManagerOpenRangeIdentity(b *testing.B) {
	ctx, mgr, plan, totalSize := benchmarkRangeFixture(b)
	rangeSize := int64(512 << 10)

	b.SetBytes(rangeSize)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		offset := int64(i) % (totalSize - rangeSize)
		reader, _, err := mgr.OpenRange(ctx, plan.TransferID, offset, rangeSize)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, reader); err != nil {
			_ = reader.Close()
			b.Fatal(err)
		}
		_ = reader.Close()
	}
}

func BenchmarkManagerForEachRangeIdentity(b *testing.B) {
	ctx, mgr, plan, totalSize := benchmarkRangeFixture(b)
	rangeSize := int64(512 << 10)

	b.SetBytes(rangeSize)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		offset := int64(i) % (totalSize - rangeSize)
		if _, err := mgr.ForEachRange(ctx, plan.TransferID, offset, rangeSize, func(part RangePart) error {
			_, err := io.Copy(io.Discard, part.Reader)
			return err
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkRangeFixture(b *testing.B) (context.Context, *Manager, TransferPlan, int64) {
	b.Helper()
	ctx := benchmarkContext()
	partSize := int64(256 << 10)
	parts := 16
	totalSize := int64(parts) * partSize
	mgr := newBenchmarkManager(b, nil, nil)
	plan := benchmarkPlan("open-range", totalSize, partSize, parts)
	if err := mgr.state.SavePlan(ctx, plan); err != nil {
		b.Fatal(err)
	}
	receipts := make([]PartReceipt, 0, parts)
	for i := range parts {
		payload := bulkBenchmarkPayload(int(partSize))
		receipt, err := mgr.AcceptPart(ctx, plan.TransferID, PartDescriptor{
			PartNumber:        i,
			Offset:            int64(i) * partSize,
			Size:              partSize,
			ExpectedRawSHA256: sha256HexBytes(payload),
		}, bytes.NewReader(payload))
		if err != nil {
			b.Fatal(err)
		}
		receipts = append(receipts, receipt)
	}
	if _, err := mgr.Complete(ctx, plan.TransferID, CompleteRequest{ExpectedRootSHA256: manifestRoot(receipts)}); err != nil {
		b.Fatal(err)
	}
	return ctx, mgr, plan, totalSize
}

func newBenchmarkManager(b *testing.B, cache CacheStore, bus EventBus) *Manager {
	b.Helper()
	store := objectstore.New(runtimeconfig.ObjectStorageConfig{
		Endpoint: "memory://bulk-bench",
		Bucket:   "bulk-bench",
	})
	mgr, err := NewManager(Options{
		ObjectStore:      store,
		Cache:            cache,
		EventBus:         bus,
		DefaultChunkSize: 4 << 20,
		MaxChunkSize:     8 << 20,
		MaxParts:         DefaultMaxParts,
		Clock:            benchmarkNow,
	})
	if err != nil {
		b.Fatal(err)
	}
	return mgr
}

func benchmarkPlan(transferID string, totalSize, partSize int64, parts int) TransferPlan {
	return TransferPlan{
		TransferID:     transferID,
		OrganizationID: "org_bench",
		CorrelationID:  "corr_bench",
		TotalSize:      totalSize,
		ChunkSize:      partSize,
		MaxMemory:      partSize,
		MaxParts:       parts + 1,
		ContentType:    "application/octet-stream",
		Compression:    EncodingIdentity,
		ObjectPrefix:   "bulk/org_bench/" + transferID,
		ManifestKey:    "bulk/org_bench/" + transferID + "/manifest.json",
		State:          StateInitiated,
		CreatedAt:      benchmarkNow(),
		Deadline:       benchmarkDeadline(),
	}
}

func benchmarkReceipts(plan TransferPlan, parts int, partSize int64) []PartReceipt {
	receipts := make([]PartReceipt, 0, parts)
	for i := range parts {
		digest := sha256HexBytes(fmt.Appendf(nil, "part-%06d", i))
		receipts = append(receipts, PartReceipt{
			TransferID:     plan.TransferID,
			OrganizationID: plan.OrganizationID,
			CorrelationID:  plan.CorrelationID,
			PartNumber:     i,
			Offset:         int64(i) * partSize,
			RawSize:        partSize,
			EncodedSize:    partSize,
			RawSHA256:      digest,
			EncodedSHA256:  digest,
			Encoding:       EncodingIdentity,
			ObjectKey:      partObjectKey(plan, i),
			CreatedAt:      benchmarkNow(),
		})
	}
	return receipts
}

func benchmarkContext() context.Context {
	ctx := security.ContextWithOrganizationID(context.Background(), "org_bench")
	md := metadata.New()
	md.CorrelationID = "corr_bench"
	md.GlobalContext = &metadata.GlobalContext{OrganizationID: "org_bench", UserID: "user_bench"}
	return metadata.IntoContext(ctx, md)
}

func benchmarkNow() time.Time {
	return time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
}

func benchmarkDeadline() time.Time {
	return benchmarkNow().Add(time.Hour)
}

func bulkBenchmarkPayload(size int) []byte {
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte((i*17 + i/11) % 251)
	}
	return payload
}

func bulkCompressiblePayload(size int) []byte {
	pattern := []byte("foundation-bulk-transfer-")
	payload := make([]byte, size)
	for offset := 0; offset < len(payload); {
		offset += copy(payload[offset:], pattern)
	}
	return payload
}

func sha256HexBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return sha256BytesHex(sum)
}
