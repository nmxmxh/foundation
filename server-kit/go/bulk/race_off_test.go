//go:build !race

package bulk

// raceDetectorEnabled reports whether this test binary was built with -race.
const raceDetectorEnabled = false
