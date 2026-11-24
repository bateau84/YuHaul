//go:build race

package pipeline

// raceEnabled reports whether the race detector is compiled in. The P1 scale
// test measures wall-clock targets that the detector's slowdown makes
// meaningless, so under it the test still runs at full size but only reports.
const raceEnabled = true
