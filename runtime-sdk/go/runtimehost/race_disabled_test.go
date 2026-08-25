//go:build !race

package runtimehost

// raceDetectorEnabled reports whether this binary was built with -race.
//
// The race detector instruments every memory access and allocates its own
// shadow state, so a test asserting an allocation budget measures the detector
// rather than the code under test. Tests with such budgets consult this instead
// of loosening the budget, which would leave them passing under -race while no
// longer guarding anything under a normal build.
const raceDetectorEnabled = false
