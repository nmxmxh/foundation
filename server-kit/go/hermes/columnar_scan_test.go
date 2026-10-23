package hermes

import (
	"math/rand"
	"testing"
	"time"
)

// TestScanDelimiterMatchesScalarReference tests parity against the scalar reference across various lengths.
func TestScanDelimiterMatchesScalarReference(t *testing.T) {
	lengths := []int{0, 1, 2, 3, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 65, 127, 128, 129, 255, 256, 512, 1024}

	for _, n := range lengths {
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = 'a'
		}

		// Subtest 1: No delimiter present.
		if got := ScanDelimiter2(buf, ',', ';'); got != -1 {
			t.Fatalf("len %d: expected -1, got %d", n, got)
		}
		if got := scanDelimiter2SWAR(buf, ',', ';'); got != -1 {
			t.Fatalf("len %d: SWAR expected -1, got %d", n, got)
		}
		if got := ScanDelimiter4(buf, ',', ';', '\t', '\n'); got != -1 {
			t.Fatalf("len %d: Delimiter4 expected -1, got %d", n, got)
		}
		if got := scanDelimiter4SWAR(buf, ',', ';', '\t', '\n'); got != -1 {
			t.Fatalf("len %d: SWAR Delimiter4 expected -1, got %d", n, got)
		}
		if got := ScanByte(buf, ','); got != -1 {
			t.Fatalf("len %d: ScanByte expected -1, got %d", n, got)
		}
		if got := scanByteSWAR(buf, ','); got != -1 {
			t.Fatalf("len %d: SWAR ScanByte expected -1, got %d", n, got)
		}

		// Subtest 2: Single delimiter placed at every position.
		for pos := 0; pos < n; pos++ {
			buf[pos] = ','
			want := scanDelimiter2Scalar(buf, ',', ';')
			gotKernel := ScanDelimiter2(buf, ',', ';')
			gotSWAR := scanDelimiter2SWAR(buf, ',', ';')
			if gotKernel != want || gotSWAR != want {
				t.Fatalf("len %d pos %d: want %d, kernel=%d, swar=%d", n, pos, want, gotKernel, gotSWAR)
			}

			// Delimiter 4 check
			want4 := scanDelimiter4Scalar(buf, ',', ';', '\t', '\n')
			got4Kernel := ScanDelimiter4(buf, ',', ';', '\t', '\n')
			got4SWAR := scanDelimiter4SWAR(buf, ',', ';', '\t', '\n')
			if got4Kernel != want4 || got4SWAR != want4 {
				t.Fatalf("len %d pos %d (4-way): want %d, kernel=%d, swar=%d", n, pos, want4, got4Kernel, got4SWAR)
			}

			// ScanByte check
			wantByte := scanByteScalar(buf, ',')
			gotByteKernel := ScanByte(buf, ',')
			gotByteSWAR := scanByteSWAR(buf, ',')
			if gotByteKernel != wantByte || gotByteSWAR != wantByte {
				t.Fatalf("len %d pos %d (byte): want %d, kernel=%d, swar=%d", n, pos, wantByte, gotByteKernel, gotByteSWAR)
			}

			buf[pos] = 'a' // restore
		}

		// Subtest 3: Multiple delimiters present (must return first occurrence).
		if n >= 4 {
			buf[1] = ';'
			buf[n-1] = ','
			if got := ScanDelimiter2(buf, ',', ';'); got != 1 {
				t.Fatalf("len %d multiple: expected first index 1, got %d", n, got)
			}
			if got := scanDelimiter2SWAR(buf, ',', ';'); got != 1 {
				t.Fatalf("len %d multiple: SWAR expected first index 1, got %d", n, got)
			}
			buf[1] = 'a'
			buf[n-1] = 'a'
		}
	}
}

// TestScanDelimiterFuzz validates random buffer patterns against the scalar reference.
func TestScanDelimiterFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for i := 0; i < 2000; i++ {
		length := rng.Intn(512)
		buf := make([]byte, length)
		rng.Read(buf)

		// Choose two random target delimiter bytes.
		b1 := byte(rng.Intn(256))
		b2 := byte(rng.Intn(256))

		want2 := scanDelimiter2Scalar(buf, b1, b2)
		got2Kernel := ScanDelimiter2(buf, b1, b2)
		got2SWAR := scanDelimiter2SWAR(buf, b1, b2)

		if got2Kernel != want2 || got2SWAR != want2 {
			t.Fatalf("fuzz iteration %d (len %d): want %d, kernel=%d, swar=%d", i, length, want2, got2Kernel, got2SWAR)
		}

		b3 := byte(rng.Intn(256))
		b4 := byte(rng.Intn(256))
		want4 := scanDelimiter4Scalar(buf, b1, b2, b3, b4)
		got4Kernel := ScanDelimiter4(buf, b1, b2, b3, b4)
		got4SWAR := scanDelimiter4SWAR(buf, b1, b2, b3, b4)
		if got4Kernel != want4 || got4SWAR != want4 {
			t.Fatalf("fuzz iteration %d (4-way len %d): want %d, kernel=%d, swar=%d", i, length, want4, got4Kernel, got4SWAR)
		}

		wantByte := scanByteScalar(buf, b1)
		gotByteKernel := ScanByte(buf, b1)
		gotByteSWAR := scanByteSWAR(buf, b1)
		if gotByteKernel != wantByte || gotByteSWAR != wantByte {
			t.Fatalf("fuzz iteration %d (byte len %d): want %d, kernel=%d, swar=%d", i, length, wantByte, gotByteKernel, gotByteSWAR)
		}
	}
}

// TestScanDelimiterZeroAllocations asserts that all scan operations do not allocate heap memory.
func TestScanDelimiterZeroAllocations(t *testing.T) {
	buf := make([]byte, 1024)
	for i := range buf {
		buf[i] = 'x'
	}
	buf[1000] = ','

	allocs := testing.AllocsPerRun(100, func() {
		_ = ScanDelimiter2(buf, ',', ';')
		_ = ScanDelimiter4(buf, ',', ';', '\t', '\n')
		_ = ScanByte(buf, ',')
	})

	if allocs != 0 {
		t.Fatalf("expected 0 allocs/run, got %f", allocs)
	}
}

// BenchmarkScanDelimiter2 compares the scalar reference, SWAR, and kernel on 4 KiB and 64 KiB buffers.
func BenchmarkScanDelimiter2(b *testing.B) {
	for _, size := range []int{64, 1024, 4096, 65536} {
		buf := make([]byte, size)
		for i := range buf {
			buf[i] = 'A'
		}
		// Place delimiter at the end to benchmark worst-case scan throughput.
		buf[size-1] = ';'

		b.Run("scalar_"+sizeString(size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				_ = scanDelimiter2Scalar(buf, ',', ';')
			}
		})

		b.Run("swar_"+sizeString(size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				_ = scanDelimiter2SWAR(buf, ',', ';')
			}
		})

		b.Run("kernel_"+sizeString(size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				_ = ScanDelimiter2(buf, ',', ';')
			}
		})
	}
}

func sizeString(n int) string {
	switch {
	case n >= 1024*1024:
		return "1MiB"
	case n >= 1024:
		return "KiB"
	default:
		return "64B"
	}
}
