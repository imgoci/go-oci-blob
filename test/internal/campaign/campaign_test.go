package campaign

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestThrottleResultExcludesUnrelatedCleanupRequests prevents separate upload
// sessions from becoming false retry evidence merely because DELETE lacks a
// non-secret request identity.
func TestThrottleResultExcludesUnrelatedCleanupRequests(t *testing.T) {
	observer := newWireObserver("registry.example.test", DefaultParallelWorkers)
	runner := &campaignRunner{observer: observer}
	observer.events = []WireEvent{
		{Phase: FeatureWrongDigest, Method: http.MethodDelete, Endpoint: endpointUpload, Status: 500},
		{Phase: FeatureWrongDigest, Method: http.MethodDelete, Endpoint: endpointUpload, Status: 204},
	}

	result := runner.throttleResult()

	assert.Equal(t, StatusNotApplicable, result.Status)
}

// TestThrottleResultRecognizesSameRequestRetry keeps useful opportunistic
// evidence for one failed request followed by a matching successful attempt.
func TestThrottleResultRecognizesSameRequestRetry(t *testing.T) {
	observer := newWireObserver("registry.example.test", DefaultParallelWorkers)
	runner := &campaignRunner{observer: observer}
	observer.events = []WireEvent{
		{Phase: FeatureMonolithicPush, Method: http.MethodPut, Endpoint: endpointUpload, Status: 500},
		{Phase: FeatureMonolithicPush, Method: http.MethodPut, Endpoint: endpointUpload, Status: 201},
	}

	result := runner.throttleResult()

	assert.Equal(t, StatusPass, result.Status)
	assert.Equal(t, "observed", result.Qualifier)
}
