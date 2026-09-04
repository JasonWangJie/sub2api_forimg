package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type schedulingImageCircuitBreakerStub struct {
	open map[int64]bool
}

func (b *schedulingImageCircuitBreakerStub) IsOpen(_ context.Context, accountID int64, _ string) (bool, error) {
	return b.open[accountID], nil
}

func (b *schedulingImageCircuitBreakerStub) RecordFailure(context.Context, int64, string) (bool, error) {
	return false, nil
}

func (b *schedulingImageCircuitBreakerStub) RecordSuccess(context.Context, int64, string) error {
	return nil
}

func TestOpenAIImageSchedulingSkipsCircuitOpenAccountsForSyncAndAsync(t *testing.T) {
	groupID := int64(1)
	accounts := []Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1},
	}
	breaker := &schedulingImageCircuitBreakerStub{open: map[int64]bool{1: true}}
	svc := &OpenAIGatewayService{
		accountRepo:         stubOpenAIAccountRepo{accounts: accounts},
		concurrencyService:  NewConcurrencyService(stubConcurrencyCache{}),
		imageCircuitBreaker: breaker,
	}

	selection, err := svc.SelectAccountWithLoadAwareness(WithOpenAIImageGenerationIntent(context.Background()), &groupID, "", "gpt-image-1", nil)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(2), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	capture := &AsyncImageAccountAttemptCapture{}
	asyncCtx := WithAsyncImageAccountAttemptCapture(context.Background(), capture)
	selection, err = svc.SelectAccountWithLoadAwareness(asyncCtx, &groupID, "", "gpt-image-1", nil)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(2), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestGatewayImageStickyLookupSkipsCircuitOpenAccounts(t *testing.T) {
	account := Account{ID: 9, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	svc := &GatewayService{
		accountRepo:         stubOpenAIAccountRepo{accounts: []Account{account}},
		imageCircuitBreaker: &schedulingImageCircuitBreakerStub{open: map[int64]bool{account.ID: true}},
	}

	selected, err := svc.getSchedulableAccount(WithGeminiImageGenerationIntent(context.Background()), account.ID)
	require.NoError(t, err)
	require.Nil(t, selected)

	selected, err = svc.getSchedulableAccount(WithAsyncImageAccountAttemptCapture(context.Background(), &AsyncImageAccountAttemptCapture{}), account.ID)
	require.NoError(t, err)
	require.Nil(t, selected)
}
