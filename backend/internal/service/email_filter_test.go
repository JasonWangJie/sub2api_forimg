package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeEmailFilter(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty", input: "", want: ""},
		{name: "trims lowercases and removes empty entries", input: " User@Example.COM ; ;other@example.com;", want: "user@example.com;other@example.com"},
		{name: "deduplicates case insensitively", input: "user@example.com;USER@example.com", want: "user@example.com"},
		{name: "rejects invalid address", input: "user@example.com;not-an-email", wantErr: true},
		{name: "rejects display name", input: "User <user@example.com>", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeEmailFilter(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestEmailFilterContainsUsesCaseInsensitiveExactMatch(t *testing.T) {
	filter := "blocked@example.com;other@example.org"
	require.True(t, emailFilterContains(filter, "BLOCKED@example.com"))
	require.True(t, emailFilterContains(filter, "Blocked User <blocked@example.com>"))
	require.False(t, emailFilterContains(filter, "prefix-blocked@example.com"))
	require.False(t, emailFilterContains(filter, "blocked+tag@example.com"))
	require.False(t, emailFilterContains(filter, "blocked@example.org"))
}

func TestSendEmailSuppressesFilteredRecipientBeforeSMTPConfiguration(t *testing.T) {
	repo := newNotificationEmailMemorySettingRepo()
	require.NoError(t, repo.Set(context.Background(), SettingKeyEmailFilter, "blocked@example.com"))
	svc := NewEmailService(repo, nil)

	require.NoError(t, svc.SendEmail(context.Background(), "BLOCKED@example.com", "subject", "body"))

	err := svc.SendEmail(context.Background(), "allowed@example.com", "subject", "body")
	require.ErrorIs(t, err, ErrEmailNotConfigured)
}

type failingEmailFilterSettingRepo struct {
	*notificationEmailMemorySettingRepo
}

func (r *failingEmailFilterSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, errors.New("settings unavailable")
}

func TestSendEmailFailsClosedWhenFilterCannotBeRead(t *testing.T) {
	repo := &failingEmailFilterSettingRepo{notificationEmailMemorySettingRepo: newNotificationEmailMemorySettingRepo()}
	svc := NewEmailService(repo, nil)

	err := svc.SendEmail(context.Background(), "allowed@example.com", "subject", "body")
	require.ErrorContains(t, err, "get email filter setting")
}
