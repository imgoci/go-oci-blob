package campaign

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/opencontainers/go-digest"
)

const (
	// imageConfigMediaType is the OCI image config descriptor media type.
	imageConfigMediaType = "application/vnd.oci.image.config.v1+json"
	// imageLayerMediaType is the portable OCI layer descriptor media type.
	imageLayerMediaType = "application/vnd.oci.image.layer.v1.tar"
	// imageManifestMediaType is the OCI image manifest media type.
	imageManifestMediaType = "application/vnd.oci.image.manifest.v1+json"
)

// orasVerifier executes the independent CLI without putting secrets in argv.
type orasVerifier struct {
	// binary is the executable path or command name.
	binary string
	// registryConfig is a caller-created authentication file.
	registryConfig string
	// caFile is optional registry trust material.
	caFile string
	// workDir holds short-lived fetched bytes.
	workDir string
}

// imageManifest is the minimal independent reference used to make a library-
// pushed blob retrievable on registries that hide unreferenced layers.
type imageManifest struct {
	// SchemaVersion is the OCI image-manifest schema version.
	SchemaVersion int `json:"schemaVersion"`
	// MediaType identifies the document as an OCI image manifest.
	MediaType string `json:"mediaType"`
	// Config is the independently pushed empty image config.
	Config manifestDescriptor `json:"config"`
	// Layers references the library-pushed blob without uploading it again.
	Layers []manifestDescriptor `json:"layers"`
}

// manifestDescriptor is one content descriptor in imageManifest.
type manifestDescriptor struct {
	// MediaType describes the referenced bytes.
	MediaType string `json:"mediaType"`
	// Digest is the content address.
	Digest string `json:"digest"`
	// Size is the exact byte length.
	Size int64 `json:"size"`
}

// version returns a single-line redacted ORAS identity.
func (verifier orasVerifier) version(ctx context.Context) (string, error) {
	// The operator-selected executable is part of the reviewed campaign config.
	//nolint:gosec // Reviewed config selects the executable.
	output, err := exec.CommandContext(ctx, verifier.binary, "version").CombinedOutput()
	if err != nil {
		return "", errors.New("running ORAS version: command failed")
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "", errors.New("ORAS version returned no output")
	}
	return sanitizeText(lines[0]), nil
}

// verifyBlob fetches a blob with ORAS and checks exact bytes and digest.
func (verifier orasVerifier) verifyBlob(
	ctx context.Context,
	host, repository string,
	dgst digest.Digest,
	want []byte,
) error {
	if err := os.MkdirAll(verifier.workDir, 0o700); err != nil {
		return fmt.Errorf("creating ORAS work directory: %w", err)
	}
	output, err := os.CreateTemp(verifier.workDir, "oras-blob-*.bin")
	if err != nil {
		return fmt.Errorf("creating ORAS output: %w", err)
	}
	outputPath := output.Name()
	if closeErr := output.Close(); closeErr != nil {
		return fmt.Errorf("closing ORAS output: %w", closeErr)
	}
	defer os.Remove(outputPath)
	arguments := []string{"blob", "fetch", "--no-tty", "--output", outputPath,
		"--registry-config", verifier.registryConfig}
	if verifier.caFile != "" {
		arguments = append(arguments, "--ca-file", verifier.caFile)
	}
	arguments = append(arguments, host+"/"+repository+"@"+dgst.String())
	// All dynamic arguments are operator-reviewed paths or registry identifiers;
	// the command is executed directly without a shell.
	//nolint:gosec // Direct exec uses reviewed config and no shell.
	command := exec.CommandContext(
		ctx,
		verifier.binary,
		arguments...)
	command.Env = append(os.Environ(), "NO_COLOR=1")
	if runErr := command.Run(); runErr != nil {
		return errors.New("ORAS blob fetch failed")
	}
	got, err := os.ReadFile(filepath.Clean(outputPath))
	if err != nil {
		return fmt.Errorf("reading ORAS output: %w", err)
	}
	if !bytes.Equal(got, want) {
		return errors.New("ORAS bytes differed")
	}
	if digest.FromBytes(got) != dgst {
		return errors.New("ORAS digest differed")
	}
	return nil
}

// linkBlob pushes only a tiny independent config and manifest that references
// an already-present library blob; it never uploads the blob under test.
func (verifier orasVerifier) linkBlob(
	ctx context.Context,
	host, repository string,
	value fixture,
) error {
	if err := os.MkdirAll(verifier.workDir, 0o700); err != nil {
		return fmt.Errorf("creating ORAS work directory: %w", err)
	}
	configBytes := []byte("{}")
	configDigest := digest.FromBytes(configBytes)
	configPath, err := verifier.writeTemporary("oras-config-*.json", configBytes)
	if err != nil {
		return err
	}
	defer os.Remove(configPath)
	configArguments := verifier.registryArguments("blob", "push", "--no-tty", "--media-type", imageConfigMediaType)
	configArguments = append(configArguments, host+"/"+repository+"@"+configDigest.String(), configPath)
	if runErr := verifier.run(ctx, configArguments); runErr != nil {
		return fmt.Errorf("ORAS config blob push: %w", runErr)
	}
	manifestBytes, err := json.Marshal(imageManifest{
		SchemaVersion: 2,
		MediaType:     imageManifestMediaType,
		Config: manifestDescriptor{
			MediaType: imageConfigMediaType,
			Digest:    configDigest.String(),
			Size:      int64(len(configBytes)),
		},
		Layers: []manifestDescriptor{{
			MediaType: imageLayerMediaType,
			Digest:    value.digest.String(),
			Size:      int64(len(value.data)),
		}},
	})
	if err != nil {
		return fmt.Errorf("encoding ORAS link manifest: %w", err)
	}
	manifestPath, err := verifier.writeTemporary("oras-manifest-*.json", manifestBytes)
	if err != nil {
		return err
	}
	defer os.Remove(manifestPath)
	tag := "compat-" + value.digest.Encoded()[:20]
	manifestArguments := verifier.registryArguments(
		"manifest", "push", "--media-type", imageManifestMediaType,
	)
	manifestArguments = append(manifestArguments, host+"/"+repository+":"+tag, manifestPath)
	if runErr := verifier.run(ctx, manifestArguments); runErr != nil {
		return fmt.Errorf("ORAS manifest link push: %w", runErr)
	}
	return nil
}

// registryArguments adds non-secret authentication and trust paths to an ORAS command.
func (verifier orasVerifier) registryArguments(arguments ...string) []string {
	result := append([]string{}, arguments...)
	result = append(result, "--registry-config", verifier.registryConfig)
	if verifier.caFile != "" {
		result = append(result, "--ca-file", verifier.caFile)
	}
	return result
}

// writeTemporary creates one private ORAS control artifact.
func (verifier orasVerifier) writeTemporary(pattern string, data []byte) (string, error) {
	file, err := os.CreateTemp(verifier.workDir, pattern)
	if err != nil {
		return "", fmt.Errorf("creating ORAS control file: %w", err)
	}
	path := file.Name()
	if _, err := file.Write(data); err != nil {
		return "", errors.Join(fmt.Errorf("writing ORAS control file: %w", err), file.Close())
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("closing ORAS control file: %w", err)
	}
	return path, nil
}

// run executes ORAS without a shell and suppresses output that might contain
// provider URLs or token-service details.
func (verifier orasVerifier) run(ctx context.Context, arguments []string) error {
	//nolint:gosec // Direct exec uses reviewed config and no shell.
	command := exec.CommandContext(ctx, verifier.binary, arguments...)
	command.Env = append(os.Environ(), "NO_COLOR=1")
	if err := command.Run(); err != nil {
		return errors.New("command failed")
	}
	return nil
}
