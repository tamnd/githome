//go:build !race

package fe_test

// raceEnabled is false in a normal (non -race) build, so the SLO gate enforces
// its wall-clock budget.
const raceEnabled = false
