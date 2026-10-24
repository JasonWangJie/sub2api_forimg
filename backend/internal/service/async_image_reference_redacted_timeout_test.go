package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const referenceRedactedNetworkTestMessage = `Error while downloading https://***.com/***/***/*** Get "https://***.com/***/***/*** dial tcp ***.***.***.***:443: i/o timeout`

func TestAsyncImageReferenceRedactedTimeoutIsLimitedToObservedShape(t *testing.T) {
	for _, test := range []struct {
		name, message string
		want          bool
	}{
		{"known redacted Go timeout", referenceRedactedNetworkTestMessage, true},
		{"normalized whitespace", " \n" + strings.ReplaceAll(referenceRedactedNetworkTestMessage, " Get ", "\tGet\n") + "\t", true},
		{"case normalization", strings.ToUpper(referenceRedactedNetworkTestMessage), true},
		{"different URLs", strings.Replace(referenceRedactedNetworkTestMessage, ".com", ".net", 1), false},
		{"partially visible host", strings.ReplaceAll(referenceRedactedNetworkTestMessage, "***.com", "images.example.com"), false},
		{"partially visible path", strings.ReplaceAll(referenceRedactedNetworkTestMessage, "/***/***/***", "/public/***/***"), false},
		{"wrong masked path", strings.ReplaceAll(referenceRedactedNetworkTestMessage, "/***/***/***", "/***/***"), false},
		{"non HTTPS", strings.ReplaceAll(referenceRedactedNetworkTestMessage, "https://", "http://"), false},
		{"unredacted malformed URLs", strings.ReplaceAll(referenceRedactedNetworkTestMessage, "https://***.com/***/***/***", "https://references.example/input.png"), false},
		{"unredacted endpoint", strings.Replace(referenceRedactedNetworkTestMessage, "***.***.***.***:443", "203.0.113.4:443", 1), false},
		{"other port", strings.Replace(referenceRedactedNetworkTestMessage, ":443:", ":8443:", 1), false},
		{"missing opening quote", strings.Replace(referenceRedactedNetworkTestMessage, `Get "`, "Get ", 1), false},
		{"generation prefix", "Generation output download: " + referenceRedactedNetworkTestMessage, false},
		{"additional refusal", referenceRedactedNetworkTestMessage + " Do not automatically regenerate.", false},
		{"unknown outcome", referenceRedactedNetworkTestMessage + " Generation may already have been charged.", false},
		{"permanent failure", referenceRedactedNetworkTestMessage + " HTTP 403 forbidden", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, isAsyncImageReferenceNetworkDownloadFailure(test.message))
			require.Equal(t, test.want, IsAsyncImageReferenceAccountFailoverMessage(400, test.message))
		})
	}
	require.False(t, IsAsyncImageReferenceAccountFailoverMessage(502, referenceRedactedNetworkTestMessage))
	require.False(t, IsAsyncImageReferenceAccountFailoverMessage(504, referenceRedactedNetworkTestMessage))
}

func TestAsyncImageReferenceRedactedTimeoutRequiresInputContextAndKeepsSafeAudit(t *testing.T) {
	body := []byte(referenceBudgetTestBody(referenceRedactedNetworkTestMessage))
	require.False(t, isAsyncImageReferenceAccountFailoverForContext(context.Background(), 400, body))
	require.False(t, isAsyncImageReferenceAccountFailoverForContext(WithAsyncImageInputReferenceTask(context.Background(), false), 400, body))
	require.True(t, isAsyncImageReferenceAccountFailoverForContext(WithAsyncImageInputReferenceTask(context.Background(), true), 400, body))
	failure := newAsyncImageReferenceAccountFailoverError(body, nil)
	require.True(t, failure.IsAsyncImageReferenceAccountFailover())
	require.False(t, failure.RetryableOnSameAccount)
	require.Equal(t, "reference_download_error", failure.ProviderErrorCode)
	require.NotContains(t, failure.ProviderErrorMessage, ".com")
	require.NotContains(t, failure.ProviderErrorMessage, "/***/***/***")
	require.True(t, IsAsyncImageReferenceAccountFailoverMessage(400, failure.ProviderErrorMessage))
}

func TestAsyncImageReferenceLocalFallbackContextIsPrivateAndDefaultsOff(t *testing.T) {
	require.Nil(t, WithAsyncImageReferenceLocalFallback(nil, true))
	require.False(t, AsyncImageReferenceLocalFallbackFromContext(nil))
	require.False(t, AsyncImageReferenceLocalFallbackFromContext(context.Background()))
	ctx := WithAsyncImageInputReferenceTask(context.Background(), true)
	ctx = WithAsyncImageReferenceLocalFallback(ctx, true)
	require.True(t, AsyncImageReferenceLocalFallbackFromContext(ctx))
	require.True(t, IsAsyncImageInputReferenceTask(ctx))
	require.False(t, AsyncImageReferenceLocalFallbackFromContext(WithAsyncImageReferenceLocalFallback(ctx, false)))
	// Fallback eligibility alone cannot establish that an unqualified timeout
	// happened during input-image fetching.
	ctx = WithAsyncImageReferenceLocalFallback(context.Background(), true)
	require.False(t, isAsyncImageReferenceAccountFailoverForContext(ctx, 400, []byte(referenceBudgetTestBody(referenceRedactedNetworkTestMessage))))
}
