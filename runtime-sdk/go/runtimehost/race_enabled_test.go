//go:build race

package runtimehost

// See race_disabled_test.go for why allocation-budget tests consult this.
const raceDetectorEnabled = true
