//go:build !race

package pipeline

// raceEnabled reports whether the race detector is compiled in. See
// stranded_perf_race_on_test.go.
const raceEnabled = false
