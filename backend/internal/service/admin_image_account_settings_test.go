//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type imageSettingsAdminRepo struct {
	longContextBillingRepoStub
	updateCalls int
}

func (r *imageSettingsAdminRepo) Update(ctx context.Context, account *Account) error {
	r.updateCalls++
	return r.longContextBillingRepoStub.Update(ctx, account)
}

func TestAdminImageSettingsRejectBeforePersistence(t *testing.T) {
	for _, operation := range []string{"create", "update", "extra", "bulk"} {
		for _, extra := range []map[string]any{
			{"image_health_group": " untrimmed "},
			{"image_input_capabilities": map[string]any{"max_images": -1}},
			{"image_input_capabilities": map[string]any{"transports": []string{"invalid"}}},
		} {
			t.Run(operation, func(t *testing.T) {
				repo := &imageSettingsAdminRepo{longContextBillingRepoStub: longContextBillingRepoStub{account: &Account{
					ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				}}}
				svc := &adminServiceImpl{accountRepo: repo}
				var err error
				switch operation {
				case "create":
					_, err = svc.CreateAccount(context.Background(), &CreateAccountInput{
						Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: extra, SkipDefaultGroupBind: true,
					})
				case "update":
					_, err = svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: extra})
				case "extra":
					err = svc.UpdateAccountExtra(context.Background(), 1, extra)
				case "bulk":
					_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: extra})
				}
				require.Error(t, err)
				require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
				require.Nil(t, repo.createdAccount)
				require.Zero(t, repo.updateCalls)
				require.Zero(t, repo.updateExtraCalls)
				require.Zero(t, repo.bulkUpdateCalls)
			})
		}
	}
}

func TestAdminImageSettingsValidateFinalMergedExtra(t *testing.T) {
	for _, operation := range []string{"update", "extra", "bulk"} {
		t.Run(operation, func(t *testing.T) {
			repo := &imageSettingsAdminRepo{longContextBillingRepoStub: longContextBillingRepoStub{account: &Account{
				ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Extra: map[string]any{"image_input_capabilities": map[string]any{"max_images": -1}},
			}}}
			svc := &adminServiceImpl{accountRepo: repo}
			var err error
			switch operation {
			case "update":
				_, err = svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Name: "rename"})
			case "extra":
				err = svc.UpdateAccountExtra(context.Background(), 1, map[string]any{"unrelated": true})
			case "bulk":
				_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
					AccountIDs: []int64{1}, Extra: map[string]any{"unrelated": true},
				})
			}
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err), "existing malformed merged capability must not be persisted")
			require.Zero(t, repo.updateCalls+repo.updateExtraCalls+repo.bulkUpdateCalls)
		})
	}
}

func TestAdminImageSettingsCreateAndReplace(t *testing.T) {
	repo := &imageSettingsAdminRepo{}
	svc := &adminServiceImpl{accountRepo: repo}
	account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Platform: PlatformOpenAI, Type: AccountTypeAPIKey, SkipDefaultGroupBind: true,
		Extra: map[string]any{"image_health_group": "provider-a", "image_input_capabilities": map[string]any{"max_images": 2}},
	})
	require.NoError(t, err)
	require.Equal(t, false, account.Extra[openAILongContextBillingEnabledKey], "existing extra normalization must remain intact")
	require.Equal(t, "provider-a", account.Extra["image_health_group"])
	account, err = svc.UpdateAccount(context.Background(), account.ID, &UpdateAccountInput{
		Extra: map[string]any{"image_input_capabilities": map[string]any{"transports": []string{"local"}, "max_images": 4}},
	})
	require.NoError(t, err)
	require.Equal(t, 1, repo.updateCalls)
	require.Equal(t, 4, account.Extra["image_input_capabilities"].(map[string]any)["max_images"])
	err = svc.UpdateAccountExtra(context.Background(), account.ID, map[string]any{"image_input_capabilities": nil})
	require.NoError(t, err)
	require.Equal(t, 1, repo.updateExtraCalls, "explicit null clears optional limits")
}
