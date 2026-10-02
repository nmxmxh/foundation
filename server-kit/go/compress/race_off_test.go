//go:build !race

package compress

// raceDetectorEnabled reports whether this test binary was built with -race.
// See race_on_test.go for why the allocation budgets skip that mode.
const raceDetectorEnabled = false
