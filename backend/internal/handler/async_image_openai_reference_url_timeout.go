package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type asyncImageOpenAIReferenceRetryAccountKey struct{}

// A later generation send consumes the local retry opportunity, even if it
// used an alternate account. Local download and scheduling failures do not.
func asyncImageOpenAIReferenceRetryAccount(attemptsJSON json.RawMessage) (accountID int64, pending bool) {
	var attempts []service.AsyncImageAccountAttempt
	if json.Unmarshal(attemptsJSON, &attempts) != nil {
		return 0, false
	}
	sent := false
	for i := len(attempts) - 1; i >= 0; i-- {
		attempt := attempts[i]
		if attempt.Status == service.AsyncImageAccountAttemptFailed && attempt.AccountID > 0 &&
			attempt.StatusCode == http.StatusBadRequest && attempt.FailureReason == string(service.AsyncImageOpenAIReferenceURLTimeoutReason) {
			return attempt.AccountID, !sent
		}
		sent = sent || attempt.Status == service.AsyncImageAccountAttemptInvoked
	}
	return 0, false
}

func withAsyncImageOpenAIReferenceRetryAccount(ctx context.Context, accountID int64) context.Context {
	return context.WithValue(ctx, asyncImageOpenAIReferenceRetryAccountKey{}, accountID)
}

func asyncImageOpenAIReferenceRetryAccountFromContext(ctx context.Context) int64 {
	id, _ := ctx.Value(asyncImageOpenAIReferenceRetryAccountKey{}).(int64)
	return id
}
