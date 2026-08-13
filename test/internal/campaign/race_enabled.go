//go:build race

package campaign

import "runtime"

// raceEnabled reports that the executable was built with the race detector.
func raceEnabled() bool {
	return true
}

// raceClean reports whether the race runtime observed no data races so far.
func raceClean() bool {
	return runtime.RaceErrors() == 0
}
