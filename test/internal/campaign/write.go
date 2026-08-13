package campaign

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"

	blob "github.com/imgoci/go-oci-blob"
)

// writeProbeResults groups the stable upload classifications.
type writeProbeResults struct {
	// small is the approximately one-KiB transfer result.
	small FeatureResult
	// unreferenced is pre-manifest independent retrieval.
	unreferenced FeatureResult
	// monolithic is the default upload result and wire shape.
	monolithic FeatureResult
	// empty is the zero-byte upload result.
	empty FeatureResult
	// chunked is the opt-in PATCH result.
	chunked FeatureResult
	// wrongDigest is the mismatch-rejection result.
	wrongDigest FeatureResult
	// exactSize is the short and trailing input result.
	exactSize FeatureResult
}

// manifestLinkOutcome is the safe causal result of an independent raw link.
type manifestLinkOutcome string

const (
	// manifestLinkSucceeded means the registry accepted the manifest.
	manifestLinkSucceeded manifestLinkOutcome = "succeeded"
	// manifestLinkBlobMissing means a stable non-auth response explicitly
	// rejected a missing referenced blob.
	manifestLinkBlobMissing manifestLinkOutcome = "blob-missing"
)

// distributionErrorEnvelope is the bounded portion of a registry error body
// needed to classify a missing manifest dependency.
type distributionErrorEnvelope struct {
	// Errors contains Distribution error codes; messages and details are ignored.
	Errors []struct {
		// Code is a standard Distribution error identifier.
		Code string `json:"code"`
	} `json:"errors"`
}

// probeWrites exercises positive and negative upload behavior.
func (runner *campaignRunner) probeWrites(ctx context.Context) (writeProbeResults, error) {
	small := newFixture(runner.cfg.Run.ID, "small", 1027)
	if _, err := runner.pushAndVerifyLinked(ctx, runner.serial, runner.source, small, FeatureSmallBlob); err != nil {
		return writeProbeResults{}, err
	}
	smallResult := featureResult(
		FeatureSmallBlob,
		StatusPass,
		"",
		"1,027 salted bytes pushed and independently retrieved exactly",
	)

	monolithic := newFixture(
		runner.cfg.Run.ID,
		"monolithic",
		max(2<<20, int(runner.cfg.Parameters.ParallelChunkBytes*5+17)),
	)
	monolithicWire, err := runner.pushOnly(ctx, runner.serial, runner.source, monolithic, FeatureMonolithicPush)
	if err != nil {
		return writeProbeResults{}, err
	}
	if !validMonolithicWire(monolithicWire.Events, int64(len(monolithic.data))) {
		return writeProbeResults{}, fmt.Errorf(
			"monolithic Push lacked POST 202 and body-bearing final PUT 201 without PATCH: %s",
			safeStatusSummary(monolithicWire.Events),
		)
	}
	unreferencedResult, err := runner.probeUnreferenced(ctx, runner.source, monolithic)
	if err != nil {
		return writeProbeResults{}, err
	}
	if verifyErr := runner.linkAndVerify(ctx, runner.source, monolithic); verifyErr != nil {
		return writeProbeResults{}, fmt.Errorf("independent verification after monolithic Push: %w", verifyErr)
	}
	monolithicResult := featureResult(
		FeatureMonolithicPush,
		StatusPass,
		"",
		"POST opened a session and one body-bearing PUT committed exact independently retrieved bytes",
	)

	emptyResult, err := runner.probeEmpty(ctx)
	if err != nil {
		return writeProbeResults{}, err
	}

	chunked := newFixture(runner.cfg.Run.ID, "chunked", int(runner.cfg.Parameters.UploadChunkBytes*3+137))
	chunkedWire, chunkedErr := runner.pushOnly(ctx, runner.chunked, runner.source, chunked, FeatureChunkedPush)
	chunkedResult, err := runner.classifyChunked(ctx, chunked, chunkedWire, chunkedErr)
	if err != nil {
		return writeProbeResults{}, err
	}

	wrongDigestResult, err := runner.probeWrongDigest(ctx)
	if err != nil {
		return writeProbeResults{}, err
	}
	exactSizeResult, err := runner.probeExactSize(ctx)
	if err != nil {
		return writeProbeResults{}, err
	}
	return writeProbeResults{
		small: smallResult, unreferenced: unreferencedResult, monolithic: monolithicResult,
		empty: emptyResult, chunked: chunkedResult,
		wrongDigest: wrongDigestResult, exactSize: exactSizeResult,
	}, nil
}

// pushOnly uploads one fixture and returns its library-only wire evidence.
func (runner *campaignRunner) pushOnly(
	ctx context.Context,
	client *blob.Client,
	repository blob.Repository,
	value fixture,
	phase string,
) (WireSnapshot, error) {
	capture := runner.observer.startPhase(phase, false)
	err := client.Push(ctx, repository, value.digest, int64(len(value.data)), bytes.NewReader(value.data))
	snapshot := capture.finish()
	if err != nil {
		return snapshot, fmt.Errorf("push %s failed: %w", value.label, err)
	}
	return snapshot, nil
}

// pushAndVerifyLinked uploads a fixture, links it independently, and verifies it.
func (runner *campaignRunner) pushAndVerifyLinked(
	ctx context.Context,
	client *blob.Client,
	repository blob.Repository,
	value fixture,
	phase string,
) (WireSnapshot, error) {
	snapshot, err := runner.pushOnly(ctx, client, repository, value, phase)
	if err != nil {
		return snapshot, err
	}
	if err := runner.linkAndVerify(ctx, repository, value); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

// linkAndVerify references a library-pushed blob without reuploading it, then
// checks exact bytes with raw HTTP and ORAS.
func (runner *campaignRunner) linkAndVerify(
	ctx context.Context,
	repository blob.Repository,
	value fixture,
) error {
	if err := runner.oras.linkBlob(ctx, runner.cfg.Registry.Host, repository.Name, value); err != nil {
		return fmt.Errorf("linking %s independently: %w", value.label, err)
	}
	if err := verifyRawExact(ctx, runner.raw, runner.cfg, repository.Name, value); err != nil {
		return fmt.Errorf("raw verification after %s: %w", value.label, err)
	}
	if err := runner.oras.verifyBlob(
		ctx,
		runner.cfg.Registry.Host,
		repository.Name,
		value.digest,
		value.data,
	); err != nil {
		return fmt.Errorf("ORAS verification after %s: %w", value.label, err)
	}
	return nil
}

// probeUnreferenced checks whether a successful library upload is immediately
// retrievable before any manifest references it.
func (runner *campaignRunner) probeUnreferenced(
	ctx context.Context,
	repository blob.Repository,
	value fixture,
) (FeatureResult, error) {
	status, body, err := rawBlob(
		ctx,
		runner.raw,
		runner.cfg,
		http.MethodGet,
		repository.Name,
		value.digest,
		int64(len(value.data)),
	)
	if err != nil {
		return FeatureResult{}, fmt.Errorf("raw unreferenced control: %w", err)
	}
	libraryAvailable := pullExact(ctx, runner.serial, repository, value) == nil
	orasAvailable := runner.oras.verifyBlob(
		ctx,
		runner.cfg.Registry.Host,
		repository.Name,
		value.digest,
		value.data,
	) == nil
	if status == http.StatusOK && bytes.Equal(body, value.data) && libraryAvailable && orasAvailable {
		return featureResult(
			FeatureUnreferenced,
			StatusPass,
			"observed",
			"library Pull, raw HTTP, and ORAS retrieved exact bytes before any manifest link",
		), nil
	}
	if status == http.StatusNotFound && !libraryAvailable && !orasAvailable {
		return featureResult(
			FeatureUnreferenced,
			StatusNo,
			"",
			"successful upload remained unavailable to library Pull, raw GET, and ORAS before manifest linking",
		), nil
	}
	return FeatureResult{}, fmt.Errorf(
		"unreferenced controls disagreed: raw status %d, library success %t, ORAS success %t",
		status,
		libraryAvailable,
		orasAvailable,
	)
}

// validMonolithicWire checks the default upload method and size shape.
func validMonolithicWire(events []WireEvent, size int64) bool {
	postAccepted := false
	putCreated := false
	for _, event := range events {
		if event.Endpoint != endpointUpload || event.Method == http.MethodDelete {
			continue
		}
		switch event.Method {
		case http.MethodPost:
			postAccepted = postAccepted || event.Status == http.StatusAccepted
		case http.MethodPatch:
			return false
		case http.MethodPut:
			putCreated = putCreated || event.Status == http.StatusCreated && event.RequestBytes == size
		}
	}
	return postAccepted && putCreated
}

// classifyChunked separates a recognized registry limitation from an invalid run.
func (runner *campaignRunner) classifyChunked(
	ctx context.Context,
	value fixture,
	wire WireSnapshot,
	pushErr error,
) (FeatureResult, error) {
	patches := filterMethod(wire.Events, http.MethodPatch)
	if pushErr == nil {
		if len(patches) < 2 || !everyStatus(patches, http.StatusAccepted) || !hasCommitPUT(wire.Events, 0) {
			return FeatureResult{}, errors.New("chunked Push succeeded without multiple PATCH 202s and empty PUT 201")
		}
		if err := runner.linkAndVerify(ctx, runner.source, value); err != nil {
			return FeatureResult{}, fmt.Errorf("independent verification after chunked Push: %w", err)
		}
		return featureResult(
			FeatureChunkedPush,
			StatusPass,
			"",
			fmt.Sprintf(
				"%d PATCH requests were acknowledged and exact bytes were independently retrieved",
				len(patches),
			),
		), nil
	}
	if len(patches) == 0 {
		return FeatureResult{}, fmt.Errorf("chunked Push failed before exercising PATCH: %w", pushErr)
	}
	visible, absenceErr := runner.pollExpectedPersistence(
		ctx,
		runner.source.Name,
		persistenceCandidate{digest: value.digest, bodies: [][]byte{value.data}},
	)
	if absenceErr != nil {
		return FeatureResult{}, fmt.Errorf("classifying chunked Push digest visibility: %w", absenceErr)
	}
	if visible {
		return featureResult(
			FeatureChunkedPush,
			StatusNo,
			"",
			"registry exposed the digest after chunked Push returned an error",
		), nil
	}
	if !hasDeleteAttempt(wire.Events) {
		return FeatureResult{}, errors.New("chunked Push failed without an upload-session cleanup attempt")
	}
	last := patches[len(patches)-1]
	recognized := !last.TransportError && (last.Status == http.StatusBadRequest ||
		last.Status == http.StatusMethodNotAllowed ||
		last.Status == http.StatusNotFound ||
		last.Status == http.StatusNotImplemented ||
		last.Status == http.StatusCreated ||
		(last.Status == http.StatusAccepted && last.ResponseRange == ""))
	if !recognized {
		return FeatureResult{}, fmt.Errorf(
			"unclassified chunked Push failure after status %d: %w",
			last.Status,
			pushErr,
		)
	}
	return featureResult(
		FeatureChunkedPush,
		StatusNo,
		"",
		fmt.Sprintf(
			"registry rejected or failed to acknowledge PATCH; digest stayed absent and cleanup DELETE was attempted (status %d)",
			last.Status,
		),
	), nil
}

// probeEmpty classifies safe registry refusal or inaccessible zero-byte data as NO.
func (runner *campaignRunner) probeEmpty(ctx context.Context) (FeatureResult, error) {
	empty := newFixture(runner.cfg.Run.ID, "empty", 0)
	capture := runner.observer.startPhase(FeatureEmptyBlob, false)
	pushErr := runner.serial.Push(ctx, runner.source, empty.digest, 0, bytes.NewReader(nil))
	wire := capture.finish()
	if pushErr == nil {
		return runner.classifyEmptySuccess(ctx, empty, wire)
	}
	if err := runner.proveEmptyUnretrievable(ctx, empty); err != nil {
		return FeatureResult{}, fmt.Errorf("empty Push failed and safe non-retrievability was not proven: %w", err)
	}
	if !recognizedSafeUploadRefusal(wire.Events) {
		return FeatureResult{}, fmt.Errorf(
			"empty Push failed without a recognized non-auth registry refusal: %w",
			pushErr,
		)
	}
	return featureResult(
		FeatureEmptyBlob,
		StatusNo,
		"",
		"registry refused the zero-byte upload and the digest stayed absent",
	), nil
}

// classifyEmptySuccess proves that a nil Push came from a real zero-byte upload,
// then independently links the blob before testing the promised Push/Pull path.
func (runner *campaignRunner) classifyEmptySuccess(
	ctx context.Context,
	empty fixture,
	wire WireSnapshot,
) (FeatureResult, error) {
	if !validMonolithicWire(wire.Events, 0) {
		return FeatureResult{}, errors.New("empty Push lacked POST 202 and zero-byte PUT 201 without PATCH")
	}
	linkOutcome, err := runner.linkEmptyRaw(ctx, empty)
	if err != nil {
		return FeatureResult{}, err
	}
	if linkOutcome == manifestLinkBlobMissing {
		if inaccessibleErr := runner.proveEmptyUnretrievable(ctx, empty); inaccessibleErr != nil {
			return FeatureResult{}, fmt.Errorf(
				"empty manifest reported a missing blob but retrieval controls disagreed: %w",
				inaccessibleErr,
			)
		}
		return featureResult(
			FeatureEmptyBlob,
			StatusNo,
			"",
			"zero-byte upload returned nil, but a non-auth manifest response reported the blob missing and every retrieval stayed unavailable",
		), nil
	}
	if err := verifyRawExact(ctx, runner.raw, runner.cfg, runner.source.Name, empty); err != nil {
		if inaccessibleErr := runner.proveEmptyUnretrievable(ctx, empty); inaccessibleErr == nil {
			return featureResult(
				FeatureEmptyBlob,
				StatusNo,
				"",
				"zero-byte upload and manifest link succeeded, but raw GET, library Pull, and ORAS stayed unavailable",
			), nil
		}
		return FeatureResult{}, fmt.Errorf("empty linked raw verification failed: %w", err)
	}
	if pullErr := pullExact(ctx, runner.serial, runner.source, empty); pullErr != nil {
		return FeatureResult{}, fmt.Errorf("empty raw control passed but library Pull failed: %w", pullErr)
	}
	if orasErr := runner.oras.verifyBlob(
		ctx,
		runner.cfg.Registry.Host,
		runner.source.Name,
		empty.digest,
		nil,
	); orasErr != nil {
		return FeatureResult{}, fmt.Errorf("empty raw control passed but ORAS failed: %w", orasErr)
	}
	return featureResult(
		FeatureEmptyBlob,
		StatusPass,
		"",
		"POST and zero-byte PUT succeeded; after independent linking, library Pull, raw HTTP, and ORAS retrieved an empty body",
	), nil
}

// linkEmptyRaw independently links the uploaded empty digest and distinguishes
// only an explicit missing-blob refusal from an invalid infrastructure result.
func (runner *campaignRunner) linkEmptyRaw(
	ctx context.Context,
	empty fixture,
) (manifestLinkOutcome, error) {
	config := fixture{
		label: "manifest-config",
		data:  []byte("{}"),
	}
	config.digest = digest.FromBytes(config.data)
	if err := verifyRawExact(ctx, runner.raw, runner.cfg, runner.source.Name, config); err != nil {
		return "", fmt.Errorf("independent manifest config prerequisite: %w", err)
	}
	manifest, err := json.Marshal(imageManifest{
		SchemaVersion: 2,
		MediaType:     imageManifestMediaType,
		Config: manifestDescriptor{
			MediaType: imageConfigMediaType,
			Digest:    config.digest.String(),
			Size:      int64(len(config.data)),
		},
		Layers: []manifestDescriptor{{
			MediaType: imageLayerMediaType,
			Digest:    empty.digest.String(),
			Size:      0,
		}},
	})
	if err != nil {
		return "", fmt.Errorf("encoding empty manifest: %w", err)
	}
	status, response, err := rawManifestPut(
		ctx,
		runner.raw,
		runner.cfg,
		runner.source.Name,
		"compat-empty",
		manifest,
	)
	if err != nil {
		return "", err
	}
	return classifyManifestLinkResponse(status, response)
}

// classifyManifestLinkResponse accepts only a successful response or an
// explicit stable missing-blob Distribution error.
func classifyManifestLinkResponse(status int, response []byte) (manifestLinkOutcome, error) {
	if status >= 200 && status < 300 {
		return manifestLinkSucceeded, nil
	}
	if status < 400 || status >= 500 || status == http.StatusUnauthorized ||
		status == http.StatusForbidden || status == http.StatusRequestTimeout ||
		status == http.StatusTooManyRequests {
		return "", fmt.Errorf("empty manifest link returned unclassified status %d", status)
	}
	var envelope distributionErrorEnvelope
	if err := json.Unmarshal(response, &envelope); err != nil {
		return "", fmt.Errorf("empty manifest refusal was not a Distribution error: %w", err)
	}
	for _, registryError := range envelope.Errors {
		if registryError.Code == "MANIFEST_BLOB_UNKNOWN" || registryError.Code == "BLOB_UNKNOWN" {
			return manifestLinkBlobMissing, nil
		}
	}
	return "", fmt.Errorf("empty manifest returned unclassified non-auth status %d", status)
}

// proveEmptyUnretrievable handles registries that virtualize the canonical
// empty digest in HEAD while returning 404 from GET.
func (runner *campaignRunner) proveEmptyUnretrievable(ctx context.Context, empty fixture) error {
	deadline := time.Now().Add(runner.durations.absenceSettle)
	for {
		status, _, err := rawBlob(ctx, runner.raw, runner.cfg, http.MethodGet, runner.source.Name, empty.digest, 1)
		if err != nil || status != http.StatusNotFound {
			return fmt.Errorf("empty raw GET returned %d: %w", status, err)
		}
		if !time.Now().Before(deadline) {
			break
		}
		timer := time.NewTimer(min(200*time.Millisecond, time.Until(deadline)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := pullExact(ctx, runner.serial, runner.source, empty); err == nil {
		return errors.New("library Pull unexpectedly retrieved the empty blob")
	}
	if err := runner.oras.verifyBlob(ctx, runner.cfg.Registry.Host, runner.source.Name, empty.digest, nil); err == nil {
		return errors.New("ORAS unexpectedly retrieved the empty blob")
	}
	return nil
}

// probeWrongDigest requires rejection and continued absence under both names.
func (runner *campaignRunner) probeWrongDigest(ctx context.Context) (FeatureResult, error) {
	value := newFixture(runner.cfg.Run.ID, "wrong-digest", 3101)
	claimed := newFixture(runner.cfg.Run.ID, "wrong-digest-claim", 3101).digest
	capture := runner.observer.startPhase(FeatureWrongDigest, false)
	err := runner.serial.Push(ctx, runner.source, claimed, int64(len(value.data)), bytes.NewReader(value.data))
	wire := capture.finish()
	if err == nil {
		return FeatureResult{}, errors.New("wrong-digest Push returned nil")
	}
	if !recognizedSafeUploadRefusal(wire.Events) {
		return FeatureResult{}, fmt.Errorf(
			"wrong-digest Push lacked a terminal non-auth registry refusal: %w",
			err,
		)
	}
	for _, candidate := range []persistenceCandidate{
		{digest: claimed, bodies: [][]byte{value.data}},
		{digest: value.digest, bodies: [][]byte{value.data}},
	} {
		visible, observationErr := runner.pollExpectedPersistence(ctx, runner.source.Name, candidate)
		if observationErr != nil {
			return FeatureResult{}, fmt.Errorf("classifying wrong-digest visibility: %w", observationErr)
		}
		if visible {
			return featureResult(
				FeatureWrongDigest,
				StatusNo,
				"",
				"registry exposed bytes after wrong-digest rejection",
			), nil
		}
	}
	if !hasDeleteAttempt(wire.Events) {
		return FeatureResult{}, errors.New("wrong-digest rejection lacked an upload-session cleanup attempt")
	}
	return featureResult(
		FeatureWrongDigest,
		StatusPass,
		"",
		"Push errored; claimed and actual digests stayed absent; cleanup DELETE was attempted",
	), nil
}

// probeExactSize checks both early EOF and trailing input under fresh digests.
func (runner *campaignRunner) probeExactSize(ctx context.Context) (FeatureResult, error) {
	short := newFixture(runner.cfg.Run.ID, "short-reader", 2049)
	trailing := newFixture(runner.cfg.Run.ID, "trailing-reader", 2051)
	tests := []struct {
		name       string
		value      fixture
		declared   int64
		candidates []digest.Digest
	}{
		{name: "short", value: short, declared: int64(len(short.data) + 1), candidates: []digest.Digest{short.digest}},
		{name: "trailing", value: trailing, declared: int64(len(trailing.data) - 1), candidates: []digest.Digest{
			digest.FromBytes(trailing.data[:len(trailing.data)-1]), trailing.digest,
		}},
	}
	for _, test := range tests {
		capture := runner.observer.startPhase(FeatureExactSize+"_"+test.name, false)
		err := runner.serial.Push(
			ctx,
			runner.source,
			test.value.digest,
			test.declared,
			bytes.NewReader(test.value.data),
		)
		wire := capture.finish()
		if err == nil || !isExactSizeError(err) {
			return FeatureResult{}, fmt.Errorf("%s reader was not rejected as an exact-size error", test.name)
		}
		if hasAuthOrTransientUploadFailure(wire.Events) {
			return FeatureResult{}, fmt.Errorf(
				"%s reader also encountered an auth or transient upload failure",
				test.name,
			)
		}
		prefix := test.value.data[:min(len(test.value.data), int(test.declared))]
		for _, candidate := range append(test.candidates, test.value.digest) {
			visible, observationErr := runner.pollExpectedPersistence(
				ctx,
				runner.source.Name,
				persistenceCandidate{digest: candidate, bodies: [][]byte{test.value.data, prefix}},
			)
			if observationErr != nil {
				return FeatureResult{}, fmt.Errorf("classifying %s reader visibility: %w", test.name, observationErr)
			}
			if visible {
				return featureResult(
					FeatureExactSize,
					StatusNo,
					"",
					fmt.Sprintf("registry persisted bytes from %s reader rejection", test.name),
				), nil
			}
		}
		if !hasDeleteAttempt(wire.Events) {
			return FeatureResult{}, fmt.Errorf(
				"%s reader rejection lacked an upload-session cleanup attempt",
				test.name,
			)
		}
	}
	return featureResult(
		FeatureExactSize,
		StatusPass,
		"",
		"short and trailing readers errored; candidate digests stayed absent; cleanup DELETEs were attempted",
	), nil
}

// isExactSizeError recognizes the library's causal reader-size diagnostics
// without matching unrelated repository names or registry messages.
func isExactSizeError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "reader size does not match declared size:")
}

// hasAuthOrTransientUploadFailure rejects network, auth, throttle, and 5xx
// outcomes that could otherwise coincide with a local reader-size error.
func hasAuthOrTransientUploadFailure(events []WireEvent) bool {
	for _, event := range events {
		if event.Endpoint != endpointUpload || event.Method == http.MethodDelete {
			continue
		}
		if event.TransportError && !event.SourceBodyError {
			return true
		}
		if event.Status == http.StatusUnauthorized ||
			event.Status == http.StatusForbidden || event.Status == http.StatusRequestTimeout ||
			event.Status == http.StatusTooManyRequests || event.Status >= 500 {
			return true
		}
	}
	return false
}

// persistenceCandidate identifies the exact bytes that a failed upload could
// have persisted under one digest.
type persistenceCandidate struct {
	// digest is the namespace queried by independent controls.
	digest digest.Digest
	// bodies lists exact known payloads that constitute unsafe persistence.
	bodies [][]byte
}

// digestObservation records either stable absence or independently fetched
// bytes without confusing an arbitrary 200 response with the test payload.
type digestObservation struct {
	// visible reports that GET returned 200.
	visible bool
	// body contains the bounded response bytes when visible is true.
	body []byte
}

// pollExpectedPersistence distinguishes stable absence from exact unsafe
// persistence across the configured settle interval.
func (runner *campaignRunner) pollExpectedPersistence(
	ctx context.Context,
	repository string,
	candidate persistenceCandidate,
) (bool, error) {
	limit := int64(0)
	for _, body := range candidate.bodies {
		limit = max(limit, int64(len(body)))
	}
	deadline := time.Now().Add(runner.durations.absenceSettle)
	for {
		observation, err := runner.observeDigest(ctx, repository, candidate.digest, limit)
		if err != nil {
			return false, err
		}
		if observation.visible {
			for _, expected := range candidate.bodies {
				if bytes.Equal(observation.body, expected) {
					return true, nil
				}
			}
			return false, errors.New("visible digest returned bytes that matched no uploaded candidate")
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		timer := time.NewTimer(min(200*time.Millisecond, time.Until(deadline)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		case <-timer.C:
		}
	}
}

// observeDigest uses independent HEAD and GET controls to distinguish a
// visible blob from clean absence without treating arbitrary statuses as data.
func (runner *campaignRunner) observeDigest(
	ctx context.Context,
	repository string,
	dgst digest.Digest,
	limit int64,
) (digestObservation, error) {
	headStatus, _, err := rawBlob(ctx, runner.raw, runner.cfg, http.MethodHead, repository, dgst, 64<<10)
	if err != nil {
		return digestObservation{}, err
	}
	getStatus, body, err := rawBlob(ctx, runner.raw, runner.cfg, http.MethodGet, repository, dgst, limit)
	if err != nil {
		return digestObservation{}, err
	}
	if getStatus == http.StatusOK {
		return digestObservation{visible: true, body: body}, nil
	}
	if headStatus == http.StatusNotFound && getStatus == http.StatusNotFound {
		return digestObservation{}, nil
	}
	return digestObservation{}, fmt.Errorf("raw HEAD/GET returned unclassified statuses %d/%d", headStatus, getStatus)
}

// filterMethod returns events with method.
func filterMethod(events []WireEvent, method string) []WireEvent {
	var filtered []WireEvent
	for _, event := range events {
		if event.Method == method {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

// everyStatus checks that every event has status.
func everyStatus(events []WireEvent, status int) bool {
	for _, event := range events {
		if event.Status != status {
			return false
		}
	}
	return len(events) > 0
}

// hasCommitPUT finds a successful final upload PUT of declared size.
func hasCommitPUT(events []WireEvent, size int64) bool {
	for _, event := range events {
		if event.Method == http.MethodPut && event.Endpoint == endpointUpload &&
			event.Status == http.StatusCreated && event.RequestBytes == size {
			return true
		}
	}
	return false
}

// hasDeleteAttempt reports whether best-effort upload cleanup reached the wire.
func hasDeleteAttempt(events []WireEvent) bool {
	for _, event := range events {
		if event.Method == http.MethodDelete && event.Endpoint == endpointUpload {
			return true
		}
	}
	return false
}

// recognizedSafeUploadRefusal requires the last non-cleanup upload event to be
// a stable non-auth 4xx response rather than a transport or retryable failure.
func recognizedSafeUploadRefusal(events []WireEvent) bool {
	var terminal *WireEvent
	for _, event := range events {
		if event.Endpoint == endpointUpload && event.Method != http.MethodDelete {
			candidate := event
			terminal = &candidate
		}
	}
	if terminal == nil || terminal.TransportError || terminal.Status < 400 || terminal.Status >= 500 {
		return false
	}
	return terminal.Status != http.StatusUnauthorized &&
		terminal.Status != http.StatusForbidden &&
		terminal.Status != http.StatusRequestTimeout &&
		terminal.Status != http.StatusTooManyRequests
}
