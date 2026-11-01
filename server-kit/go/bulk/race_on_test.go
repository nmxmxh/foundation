//go:build race

package bulk

// raceDetectorEnabled reports whether this test binary was built with -race.
// The detector drains sync.Pool caches, so allocation budgets are not asserted under it.
const raceDetectorEnabled = true
