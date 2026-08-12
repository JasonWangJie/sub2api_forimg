//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSendVerifyCodeSucceedsForFilteredRecipientWithoutSMTP(t *testing.T) {
	repo := newNotificationEmailMemorySettingRepo()
	require.NoError(t, repo.Set(context.Background(), SettingKeyEmailFilter, "blocked@example.com"))
	svc := NewEmailService(repo, &emailCacheStub{})

	require.NoError(t, svc.SendVerifyCode(context.Background(), "blocked@example.com", "Sub2API"))
}
