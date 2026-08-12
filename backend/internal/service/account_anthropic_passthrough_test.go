package service

import (
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestAccount_IsAnthropicClaudeCodeMimicEnabled(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
		want    bool
	}{
		{name: "missing defaults disabled", account: &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}},
		{name: "false is disabled", account: &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Extra: map[string]any{AnthropicClaudeCodeMimicExtraKey: false}}},
		{name: "true enables Anthropic API key", account: &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Extra: map[string]any{AnthropicClaudeCodeMimicExtraKey: true}}, want: true},
		{name: "OAuth ignores flag", account: &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{AnthropicClaudeCodeMimicExtraKey: true}}},
		{name: "other platform ignores flag", account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{AnthropicClaudeCodeMimicExtraKey: true}}},
		{name: "malformed runtime value is disabled", account: &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Extra: map[string]any{AnthropicClaudeCodeMimicExtraKey: "true"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.account.IsAnthropicClaudeCodeMimicEnabled())
		})
	}
}

func TestNormalizeAnthropicClaudeCodeMimicExtra(t *testing.T) {
	t.Run("enabled removes passthrough", func(t *testing.T) {
		extra, err := normalizeAnthropicClaudeCodeMimicExtra(PlatformAnthropic, AccountTypeAPIKey, map[string]any{
			AnthropicClaudeCodeMimicExtraKey: true,
			anthropicPassthroughExtraKey:     true,
		})
		require.NoError(t, err)
		require.Equal(t, true, extra[AnthropicClaudeCodeMimicExtraKey])
		require.NotContains(t, extra, anthropicPassthroughExtraKey)
	})

	t.Run("false is removed", func(t *testing.T) {
		extra, err := normalizeAnthropicClaudeCodeMimicExtra(PlatformAnthropic, AccountTypeAPIKey, map[string]any{
			AnthropicClaudeCodeMimicExtraKey: false,
		})
		require.NoError(t, err)
		require.NotContains(t, extra, AnthropicClaudeCodeMimicExtraKey)
	})

	t.Run("unsupported account type is removed", func(t *testing.T) {
		extra, err := normalizeAnthropicClaudeCodeMimicExtra(PlatformAnthropic, AccountTypeOAuth, map[string]any{
			AnthropicClaudeCodeMimicExtraKey: true,
		})
		require.NoError(t, err)
		require.NotContains(t, extra, AnthropicClaudeCodeMimicExtraKey)
	})

	t.Run("non boolean is a typed bad request", func(t *testing.T) {
		_, err := normalizeAnthropicClaudeCodeMimicExtra(PlatformAnthropic, AccountTypeAPIKey, map[string]any{
			AnthropicClaudeCodeMimicExtraKey: "true",
		})
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
		require.Equal(t, "ANTHROPIC_CLAUDE_CODE_MIMIC_INVALID", infraerrors.Reason(err))
	})
}

func TestAccount_IsAnthropicAPIKeyPassthroughEnabled(t *testing.T) {
	t.Run("兼容模式优先于自动透传", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAnthropic,
			Type:     AccountTypeAPIKey,
			Extra: map[string]any{
				AnthropicClaudeCodeMimicExtraKey: true,
				anthropicPassthroughExtraKey:     true,
			},
		}
		require.False(t, account.IsAnthropicAPIKeyPassthroughEnabled())
	})

	t.Run("Anthropic API Key 开启", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAnthropic,
			Type:     AccountTypeAPIKey,
			Extra: map[string]any{
				"anthropic_passthrough": true,
			},
		}
		require.True(t, account.IsAnthropicAPIKeyPassthroughEnabled())
	})

	t.Run("Anthropic API Key 关闭", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAnthropic,
			Type:     AccountTypeAPIKey,
			Extra: map[string]any{
				"anthropic_passthrough": false,
			},
		}
		require.False(t, account.IsAnthropicAPIKeyPassthroughEnabled())
	})

	t.Run("字段类型非法默认关闭", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAnthropic,
			Type:     AccountTypeAPIKey,
			Extra: map[string]any{
				"anthropic_passthrough": "true",
			},
		}
		require.False(t, account.IsAnthropicAPIKeyPassthroughEnabled())
	})

	t.Run("非 Anthropic API Key 账号始终关闭", func(t *testing.T) {
		oauth := &Account{
			Platform: PlatformAnthropic,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				"anthropic_passthrough": true,
			},
		}
		require.False(t, oauth.IsAnthropicAPIKeyPassthroughEnabled())

		openai := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Extra: map[string]any{
				"anthropic_passthrough": true,
			},
		}
		require.False(t, openai.IsAnthropicAPIKeyPassthroughEnabled())
	})
}

func TestAccount_GetAnthropicAPIKeyAuthScheme(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
		want    string
	}{
		{
			name: "missing extra defaults to x-api-key",
			account: &Account{
				Platform: PlatformAnthropic,
				Type:     AccountTypeAPIKey,
			},
			want: AnthropicAPIKeyAuthSchemeXAPIKey,
		},
		{
			name: "explicit bearer",
			account: &Account{
				Platform: PlatformAnthropic,
				Type:     AccountTypeAPIKey,
				Extra: map[string]any{
					"anthropic_apikey_auth_scheme": AnthropicAPIKeyAuthSchemeAuthorizationBearer,
				},
			},
			want: AnthropicAPIKeyAuthSchemeAuthorizationBearer,
		},
		{
			name: "invalid value defaults to x-api-key",
			account: &Account{
				Platform: PlatformAnthropic,
				Type:     AccountTypeAPIKey,
				Extra: map[string]any{
					"anthropic_apikey_auth_scheme": "bearer",
				},
			},
			want: AnthropicAPIKeyAuthSchemeXAPIKey,
		},
		{
			name: "non Anthropic API key defaults to x-api-key",
			account: &Account{
				Platform: PlatformOpenAI,
				Type:     AccountTypeAPIKey,
				Extra: map[string]any{
					"anthropic_apikey_auth_scheme": AnthropicAPIKeyAuthSchemeAuthorizationBearer,
				},
			},
			want: AnthropicAPIKeyAuthSchemeXAPIKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.account.GetAnthropicAPIKeyAuthScheme())
		})
	}
}
