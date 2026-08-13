//go:build !race

package campaign

// raceEnabled reports that the executable was built without the race detector.
func raceEnabled() bool {
	return false
}

// raceClean is false for a binary that did not run under the race detector.
func raceClean() bool {
	return false
}
