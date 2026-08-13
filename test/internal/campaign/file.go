package campaign

import (
	"fmt"
	"os"
)

// readFixtureFile reads one regular fixture file into the live campaign.
func readFixtureFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat fixture: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("fixture %s is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixture: %w", err)
	}
	return data, nil
}
