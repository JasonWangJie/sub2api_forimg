package service

import (
	"context"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// AsyncImageRetryCandidateSnapshot reads the same group/model/tier pool used
// by the worker. It does not acquire a concurrency slot or a half-open probe.
func (s *AccountService) AsyncImageRetryCandidateSnapshot(
	ctx context.Context, task *AsyncImageTask, group *Group, decision AsyncImageRetryDecision, fact AsyncImageRetryFact,
) ([]int64, error) {
	size := ""
	if task.RequestedImageSize != nil {
		size = *task.RequestedImageSize
	}
	ctx, err := WithImageAccountPoolRoute(context.WithValue(ctx, ctxkey.Group, group), task.Model, size)
	if err != nil {
		return nil, err
	}
	platforms := []string{task.Platform}
	if task.Platform == PlatformGemini {
		platforms = append(platforms, PlatformAntigravity)
	}
	accounts, configured, err := ResolveImageAccountPool(ctx, s.accountRepo, task.GroupID, platforms)
	if err != nil {
		return nil, err
	}
	if !configured {
		accounts, err = s.accountRepo.ListSchedulableByGroupIDAndPlatforms(ctx, task.GroupID, platforms)
		if err != nil {
			return nil, err
		}
		accounts = filterImageAccountPoolGroupRequirements(ctx, task.GroupID, accounts)
	}
	ids := make([]int64, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		platformEligible := account.Platform == task.Platform || (task.Platform == PlatformGemini &&
			account.Platform == PlatformAntigravity && account.IsMixedSchedulingEnabled())
		if platformEligible && account.IsModelSupported(task.Model) &&
			account.IsSchedulableForModelWithContext(ctx, task.Model) && AsyncImageRetryAccountAllowed(decision, fact, account.ID) {
			ids = append(ids, account.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}
