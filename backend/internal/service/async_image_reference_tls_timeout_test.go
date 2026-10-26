package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const referenceTLSTestMessage = `Error while downloading https://references.example/input.png?token=private Get "https://references.example/input.png?token=private": net/http: TLS handshake timeout`
const referenceRedactedTLSTestMessage = `Error while downloading https://***.com/***/***/***/*** Get "https://***.com/***/***/***/*** net/http: TLS handshake timeout`

func TestAsyncImageReferenceTLSTimeoutRequiresCompleteInputDownload(t *testing.T) {
	for _, message := range []string{referenceTLSTestMessage, referenceRedactedTLSTestMessage} {
		body := []byte(referenceBudgetTestBody(message))
		require.True(t, IsAsyncImageReferenceAccountFailoverMessage(400, message))
		require.False(t, IsAsyncImageReferenceAccountFailoverMessage(502, message))
		require.False(t, IsAsyncImageReferenceAccountFailoverMessage(504, message))
		require.False(t, isAsyncImageReferenceAccountFailoverForContext(context.Background(), 400, body))
		require.False(t, isAsyncImageReferenceAccountFailoverForContext(WithAsyncImageInputReferenceTask(context.Background(), false), 400, body))
		require.True(t, isAsyncImageReferenceAccountFailoverForContext(WithAsyncImageInputReferenceTask(context.Background(), true), 400, body))
		for _, suffix := range []string{" Do not retry.", " Rejected by content policy.", " Generation may already have been charged.", " HTTP 403 forbidden"} {
			require.False(t, IsAsyncImageReferenceAccountFailoverMessage(400, message+suffix))
		}
	}
	for _, message := range []string{
		"net/http: TLS handshake timeout",
		"Generated image download failed: " + referenceTLSTestMessage,
		strings.ReplaceAll(referenceTLSTestMessage, "https://", "http://"),
		strings.ReplaceAll(referenceTLSTestMessage, "https://", "https://user:secret@"),
		strings.Replace(referenceTLSTestMessage, "input.png", "different.png", 1),
		strings.Replace(referenceTLSTestMessage, `": net/http`, " net/http", 1),
		strings.ReplaceAll(referenceRedactedTLSTestMessage, "https://***.com/***/***/***/***", "https://visible.example/input.png"),
		strings.Replace(referenceRedactedTLSTestMessage, ".com", ".net", 1),
	} {
		require.False(t, IsAsyncImageReferenceAccountFailoverMessage(400, message), message)
	}
}

func TestAsyncImageReferenceTLSDiagnosticsKeepTimeoutKindAndHideWholeURL(t *testing.T) {
	for _, message := range []string{referenceTLSTestMessage, referenceRedactedTLSTestMessage} {
		failure := newAsyncImageReferenceAccountFailoverError([]byte(referenceBudgetTestBody(message)), nil)
		require.True(t, failure.IsAsyncImageReferenceAccountFailover())
		require.False(t, failure.RetryableOnSameAccount)
		require.Contains(t, failure.ProviderErrorMessage, "net/http: TLS handshake timeout")
		require.NotContains(t, failure.ProviderErrorMessage, "i/o timeout")
		require.NotContains(t, failure.ProviderErrorMessage, "references.example")
		require.NotContains(t, failure.ProviderErrorMessage, "private")
		require.NotContains(t, failure.ProviderErrorMessage, ".com")
		require.True(t, IsAsyncImageReferenceAccountFailoverMessage(400, failure.ProviderErrorMessage))
	}
}
