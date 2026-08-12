package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUpdateSettingsNormalizesEmailFilter(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})

	rec := doUpdateSettings(t, h, map[string]any{
		"email_filter": " User@Example.COM ; ;other@example.com;USER@example.com ",
	}, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "user@example.com;other@example.com", repo.values[service.SettingKeyEmailFilter])
}

func TestUpdateSettingsRejectsInvalidEmailFilterWithoutOverwriting(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeyEmailFilter: "existing@example.com",
	})

	rec := doUpdateSettings(t, h, map[string]any{
		"email_filter": "valid@example.com;not-an-email",
	}, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "existing@example.com", repo.values[service.SettingKeyEmailFilter])
}

func TestUpdateSettingsOmittedEmailFilterKeepsStoredValue(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeyEmailFilter: "existing@example.com",
	})

	rec := doUpdateSettings(t, h, map[string]any{"risk_control_enabled": true}, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "existing@example.com", repo.values[service.SettingKeyEmailFilter])
}

func TestUpdateSettingsExplicitEmptyEmailFilterClearsStoredValue(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeyEmailFilter: "existing@example.com",
	})

	rec := doUpdateSettings(t, h, map[string]any{"email_filter": ""}, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "", repo.values[service.SettingKeyEmailFilter])
}

func TestSendTestEmailReportsFilteredRecipientWithoutSMTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &settingHandlerRepoStub{values: map[string]string{
		service.SettingKeyEmailFilter: "blocked@example.com",
	}}
	settingService := service.NewSettingService(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	emailService := service.NewEmailService(repo, nil)
	h := NewSettingHandler(settingService, emailService, nil, nil, nil, nil, nil)

	body, err := json.Marshal(map[string]any{"email": "BLOCKED@example.com"})
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/settings/send-test-email", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.SendTestEmail(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	data, ok := payload.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, data["filtered"])
	require.Equal(t, "Recipient matched email filter; test email was not sent", data["message"])
}
