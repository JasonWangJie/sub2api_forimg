package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type apiKeyPreparedBillingRepoStub struct {
	APIKeyRepository
	key *APIKey
}

func (s *apiKeyPreparedBillingRepoStub) GetByIDIncludeDeleted(context.Context, int64) (*APIKey, error) {
	return s.key, nil
}

func TestAPIKeyServicePreparedBillingCanLoadTombstone(t *testing.T) {
	want := &APIKey{ID: 42, UserID: 7, Key: "__deleted__42"}
	apiKeys := &APIKeyService{apiKeyRepo: &apiKeyPreparedBillingRepoStub{key: want}}

	got, err := apiKeys.GetByIDForPreparedBilling(context.Background(), 42)
	require.NoError(t, err)
	require.Same(t, want, got)
}
