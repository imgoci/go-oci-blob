package campaign

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidatePrivateFileRejectsExposedOrNonRegularInputs locks the credential
// boundary shared by the library credential and independent ORAS auth config.
func TestValidatePrivateFileRejectsExposedOrNonRegularInputs(t *testing.T) {
	directory := t.TempDir()
	private := filepath.Join(directory, "private.json")
	require.NoError(t, os.WriteFile(private, []byte("{}"), 0o600))
	require.NoError(t, validatePrivateFile(private))

	exposed := filepath.Join(directory, "exposed.json")
	require.NoError(t, os.WriteFile(exposed, []byte("{}"), 0o644))
	err := validatePrivateFile(exposed)
	require.Error(t, err)
	require.ErrorContains(t, err, "allow group or other access")

	err = validatePrivateFile(directory)
	require.Error(t, err)
	require.ErrorContains(t, err, "not a regular file")
}
