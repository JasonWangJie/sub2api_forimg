package handler

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// A durable task's model, resolution and input bytes are immutable. Exclude
// only proven fixed incompatibilities for that task; transport failures may
// recover after switching to local and transient errors may recover later.
func asyncImageFixedCapabilityAccountIDs(task *service.AsyncImageTask) map[int64]struct{} {
	if task == nil {
		return nil
	}
	var attempts []service.AsyncImageAccountAttempt
	if json.Unmarshal(task.AccountAttempts, &attempts) != nil {
		return nil
	}
	ids := make(map[int64]struct{})
	for _, a := range attempts {
		if a.Status != service.AsyncImageAccountAttemptFailed || a.AccountID <= 0 {
			continue
		}
		if a.FailureReason == string(service.AsyncImageReferenceLimitReason) ||
			a.FailureReason == string(service.AsyncImageSizeCapabilityMismatchReason) ||
			service.IsAsyncImageCapabilityAccountFailure(a.StatusCode, a.ProviderErrorCode, a.Error) ||
			service.AsyncImageRetryableRejectionKind(a.StatusCode, a.ProviderErrorCode, a.Error, task.RequestType == service.AsyncImageRequestTypeImageToImage) == "reference_limit" {
			ids[a.AccountID] = struct{}{}
		}
	}
	return ids
}
