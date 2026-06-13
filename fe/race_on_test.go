//go:build race

package fe_test

// raceEnabled is true when the test binary is built with -race. The SLO gate
// reads it to skip its wall-clock budget assertion, since race instrumentation
// inflates every request well past the budget while the requests themselves
// still exercise the handlers for the race detector.
const raceEnabled = true
