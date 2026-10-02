//go:build race

package compress

// raceDetectorEnabled reports whether this test binary was built with -race.
//
// Allocation budgets must not be asserted under the race detector. The detector
// drains sync.Pool per-P caches and instruments allocations, so a pooled
// compressor that costs about 1.5 KB per call in a production build measures
// around 210 KB per call here. That is a property of the detector, not a
// regression in the pool.
const raceDetectorEnabled = true
