package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAsyncImageCapabilityAccountFailureRequiresPersistedKnownDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name, code, message string
		status              int
		want                bool
	}{
		{"size failure", "image_size_not_supported", imageSizeCapabilityTestMessage, 422, true},
		{"combined failure", "image_request_capability_not_supported", combinedCapabilityTestMessage, 422, true},
		{"normalized size", "image_size_not_supported", " \n" + strings.ToUpper(strings.TrimSuffix(imageSizeCapabilityTestMessage, ".")) + "。\t ", 422, true},
		{"normalized combined", "image_request_capability_not_supported", strings.ReplaceAll(combinedCapabilityTestMessage, ". ", ".\n\t"), 422, true},
		{"other status", "image_size_not_supported", imageSizeCapabilityTestMessage, 400, false},
		{"unknown status", "image_size_not_supported", imageSizeCapabilityTestMessage, 0, false},
		{"upstream 502", "image_request_capability_not_supported", combinedCapabilityTestMessage, 502, false},
		{"empty provider code", "", imageSizeCapabilityTestMessage, 422, false},
		{"arbitrary provider code", "invalid_size", imageSizeCapabilityTestMessage, 422, false},
		{"wrong size code", "image_request_capability_not_supported", imageSizeCapabilityTestMessage, 422, false},
		{"wrong combination code", "image_size_not_supported", combinedCapabilityTestMessage, 422, false},
		{"partial message", "image_request_capability_not_supported", "No eligible upstream image provider supports the requested capability combination.", 422, false},
		{"ordinary invalid size", "image_size_not_supported", "Size must be 1024x1024", 422, false},
		{"no regeneration", "image_size_not_supported", imageSizeCapabilityTestMessage + " Do not automatically regenerate.", 422, false},
		{"combined safety refusal", "image_request_capability_not_supported", combinedCapabilityTestMessage + " Request rejected by safety policy.", 422, false},
		{"unknown generation", "image_request_capability_not_supported", combinedCapabilityTestMessage + " Generation may already have been charged.", 422, false},
		{"reference fetch", "image_size_not_supported", "Reference image fetch failed: " + imageSizeCapabilityTestMessage, 422, false},
		{"HTTP error prefix is not a safe message", "image_size_not_supported", "HTTP 422: " + imageSizeCapabilityTestMessage, 422, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, IsAsyncImageCapabilityAccountFailure(test.status, test.code, test.message))
		})
	}
}

func TestAsyncImageCapabilityAuditPredicateDoesNotRelaxIncomingCombination(t *testing.T) {
	// A persisted attempt contains its validated safe code/message, not the full
	// response details. That must not make the incoming HTTP classifier accept
	// an unverified combination that omitted its structured capability reasons.
	require.True(t, IsAsyncImageCapabilityAccountFailure(422, "image_request_capability_not_supported", combinedCapabilityTestMessage))
	withoutDetails := []byte(fmt.Sprintf(`{"error":{"code":"image_request_capability_not_supported","message":%q}}`, combinedCapabilityTestMessage))
	require.False(t, isAsyncImageSizeCapabilityMismatch(422, withoutDetails))
	for _, body := range [][]byte{
		[]byte(imageSizeCapabilityTestBody(imageSizeCapabilityTestMessage)),
		combinedCapabilityTestBody(t, combinedCapabilityTestEnvelope(combinedCapabilityTestMessage, []string{"image_model_not_supported", "image_size_not_supported"})),
	} {
		require.True(t, isAsyncImageSizeCapabilityMismatch(422, body))
		failure := newAsyncImageSizeCapabilityFailoverError(body, nil)
		require.True(t, IsAsyncImageCapabilityAccountFailure(failure.StatusCode, failure.ProviderErrorCode, failure.ProviderErrorMessage))
	}
}

func TestAsyncImageCapabilityFailoverReasonSurvivesProviderDiagnosticVariants(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(fmt.Sprintf(`{"error":{"message":%q}}`, imageSizeCapabilityTestMessage)),
		[]byte(fmt.Sprintf(`{"error":{"status":"INVALID_ARGUMENT","code":"image_size_not_supported","message":%q}}`, imageSizeCapabilityTestMessage)),
		combinedCapabilityTestBody(t, combinedCapabilityTestEnvelope(combinedCapabilityTestMessage, []string{"image_model_not_supported", "image_size_not_supported"})),
	} {
		require.True(t, isAsyncImageSizeCapabilityMismatch(422, body))
		failure := newAsyncImageSizeCapabilityFailoverError(body, nil)
		require.Equal(t, GatewayFailureReason("async_image_size_capability_mismatch"), AsyncImageSizeCapabilityMismatchReason)
		require.Equal(t, AsyncImageSizeCapabilityMismatchReason, failure.Reason)
		require.True(t, failure.IsAsyncImageSizeCapabilityMismatch())
	}
	withStatus := []byte(fmt.Sprintf(`{"error":{"status":"INVALID_ARGUMENT","code":"image_size_not_supported","message":%q}}`, imageSizeCapabilityTestMessage))
	failure := newAsyncImageSizeCapabilityFailoverError(withStatus, nil)
	require.Equal(t, "INVALID_ARGUMENT", failure.ProviderErrorCode, "persisted reason must not overwrite the original provider diagnostics")
	// Strict legacy tuples remain strict; new attempts can use their stable
	// failure_reason even when provider diagnostics use a different code.
	require.False(t, IsAsyncImageCapabilityAccountFailure(failure.StatusCode, failure.ProviderErrorCode, failure.ProviderErrorMessage))
}
