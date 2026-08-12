package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func modelMappingRolloutTestContext(requestID string) context.Context {
	return context.WithValue(context.Background(), ctxkey.RequestID, requestID)
}

func modelMappingRolloutTestAccount(id int64, percent any) *Account {
	credentials := map[string]any{
		"model_mapping": map[string]any{
			"model-a":  "mapped-a",
			"model-*":  "mapped-wildcard",
			"identity": "identity",
		},
	}
	if percent != nil {
		credentials[ModelMappingPercentCredentialKey] = percent
	}
	return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: credentials}
}

func TestValidateModelMappingPercentCredentials(t *testing.T) {
	valid := []any{0, 1, 100, int8(50), int64(75), uint(25), float32(40), float64(30), json.Number("20"), json.Number("20.0")}
	for _, value := range valid {
		t.Run(fmt.Sprintf("valid_%T_%v", value, value), func(t *testing.T) {
			require.NoError(t, ValidateModelMappingPercentCredentials(map[string]any{ModelMappingPercentCredentialKey: value}))
		})
	}
	require.NoError(t, ValidateModelMappingPercentCredentials(nil))
	require.NoError(t, ValidateModelMappingPercentCredentials(map[string]any{}))

	invalid := []any{nil, "50", -1, 101, 1.5, float32(10.5), math.NaN(), math.Inf(1), math.MaxFloat64, uint64(math.MaxUint64), json.Number("10.5"), json.Number("invalid")}
	for _, value := range invalid {
		t.Run(fmt.Sprintf("invalid_%T_%v", value, value), func(t *testing.T) {
			err := ValidateModelMappingPercentCredentials(map[string]any{ModelMappingPercentCredentialKey: value})
			require.Error(t, err)
			require.Equal(t, 400, infraerrors.Code(err))
			require.Equal(t, "INVALID_MODEL_MAPPING_PERCENT", infraerrors.Reason(err))
		})
	}
}

func TestAccountGetModelMappingPercentDefaultsInvalidLegacyValues(t *testing.T) {
	require.Equal(t, DefaultModelMappingPercent, (*Account)(nil).GetModelMappingPercent())
	require.Equal(t, DefaultModelMappingPercent, (&Account{}).GetModelMappingPercent())
	require.Equal(t, DefaultModelMappingPercent, (&Account{Credentials: map[string]any{ModelMappingPercentCredentialKey: "bad"}}).GetModelMappingPercent())
	require.Equal(t, DefaultModelMappingPercent, (&Account{Credentials: map[string]any{ModelMappingPercentCredentialKey: 101}}).GetModelMappingPercent())
	require.Equal(t, 0, (&Account{Credentials: map[string]any{ModelMappingPercentCredentialKey: float64(0)}}).GetModelMappingPercent())
}

func TestAccountResolveMappedModelForRequestBoundariesAndRules(t *testing.T) {
	ctx := modelMappingRolloutTestContext("rollout-boundaries")

	missing := modelMappingRolloutTestAccount(1, nil)
	model, matched := missing.ResolveMappedModelForRequest(ctx, "model-a")
	require.Equal(t, "mapped-a", model)
	require.True(t, matched)

	zero := modelMappingRolloutTestAccount(1, 0)
	model, matched = zero.ResolveMappedModelForRequest(ctx, "model-a")
	require.Equal(t, "model-a", model)
	require.True(t, matched, "a bypassed explicit rule must suppress lower-priority dispatch mapping")

	hundred := modelMappingRolloutTestAccount(1, 100)
	model, matched = hundred.ResolveMappedModelForRequest(ctx, "model-a")
	require.Equal(t, "mapped-a", model)
	require.True(t, matched)

	model, matched = zero.ResolveMappedModelForRequest(ctx, "model-longer")
	require.Equal(t, "model-longer", model)
	require.True(t, matched)

	model, matched = zero.ResolveMappedModelForRequest(ctx, "identity")
	require.Equal(t, "identity", model)
	require.True(t, matched)

	model, matched = zero.ResolveMappedModelForRequest(ctx, "unlisted")
	require.Equal(t, "unlisted", model)
	require.False(t, matched)
}

func TestAccountResolveMappedModelForRequestLeavesPlatformDefaultsDeterministic(t *testing.T) {
	account := &Account{
		ID:       9,
		Platform: PlatformAntigravity,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			ModelMappingPercentCredentialKey: 0,
		},
	}
	model, matched := account.ResolveMappedModelForRequest(modelMappingRolloutTestContext("platform-default"), "claude-haiku-4-5")
	require.Equal(t, "claude-sonnet-4-6", model)
	require.True(t, matched)
}

func TestAccountResolveMappedModelForRequestIsStableAndDistributed(t *testing.T) {
	account := modelMappingRolloutTestAccount(42, 37)
	ctx := modelMappingRolloutTestContext("stable-request")
	first, firstMatched := account.ResolveMappedModelForRequest(ctx, "model-a")
	for range 20 {
		model, matched := account.ResolveMappedModelForRequest(ctx, "model-a")
		require.Equal(t, first, model)
		require.Equal(t, firstMatched, matched)
	}

	mapped := 0
	const samples = 10_000
	for index := range samples {
		requestCtx := modelMappingRolloutTestContext(fmt.Sprintf("distribution-%d", index))
		model, matched := account.ResolveMappedModelForRequest(requestCtx, "model-a")
		require.True(t, matched)
		if model == "mapped-a" {
			mapped++
		}
	}
	require.InDelta(t, 0.37, float64(mapped)/samples, 0.025)
}

func TestAccountResolveMappedModelForRequestAccountsRollIndependently(t *testing.T) {
	first := modelMappingRolloutTestAccount(1001, 50)
	second := modelMappingRolloutTestAccount(1002, 50)
	foundDifferentBranch := false
	for index := range 1000 {
		ctx := modelMappingRolloutTestContext(fmt.Sprintf("account-independent-%d", index))
		firstModel, _ := first.ResolveMappedModelForRequest(ctx, "model-a")
		secondModel, _ := second.ResolveMappedModelForRequest(ctx, "model-a")
		if firstModel != secondModel {
			foundDifferentBranch = true
			break
		}
	}
	require.True(t, foundDifferentBranch)
}

func TestOpenAIModelRuntimeBlockUsesRequestMappingBranch(t *testing.T) {
	account := modelMappingRolloutTestAccount(42, 50)
	var mappedCtx context.Context
	var originalCtx context.Context
	for index := range 1000 {
		ctx := modelMappingRolloutTestContext(fmt.Sprintf("runtime-block-branch-%d", index))
		model, _ := account.ResolveMappedModelForRequest(ctx, "model-a")
		switch model {
		case "mapped-a":
			mappedCtx = ctx
		case "model-a":
			originalCtx = ctx
		}
		if mappedCtx != nil && originalCtx != nil {
			break
		}
	}
	require.NotNil(t, mappedCtx)
	require.NotNil(t, originalCtx)

	service := &OpenAIGatewayService{}
	service.recordOpenAIAccountModelTransientFailure(account, "mapped-a", time.Now())
	service.recordOpenAIAccountModelTransientFailure(account, "mapped-a", time.Now())

	require.True(t, service.isOpenAIAccountRequestRuntimeBlockedForRequest(mappedCtx, account, "model-a"))
	require.False(t, service.isOpenAIAccountRequestRuntimeBlockedForRequest(originalCtx, account, "model-a"))
}

func TestAccountResolveMappedModelForRequestPinsWebSocketConnectionBranch(t *testing.T) {
	account := modelMappingRolloutTestAccount(42, 50)
	account.Credentials["model_mapping"] = map[string]any{
		"model-a": "mapped-a",
		"model-b": "mapped-b",
	}
	ctx := withModelMappingRolloutPin(modelMappingRolloutTestContext("websocket-connection"))

	first, _ := account.ResolveMappedModelForRequest(ctx, "model-a")
	second, _ := account.ResolveMappedModelForRequest(ctx, "model-b")
	if first == "mapped-a" {
		require.Equal(t, "mapped-b", second)
	} else {
		require.Equal(t, "model-a", first)
		require.Equal(t, "model-b", second)
	}
}

func TestAccountResolveMappedModelForRequestPinsWebSocketAccountsIndependently(t *testing.T) {
	first := modelMappingRolloutTestAccount(1001, 50)
	second := modelMappingRolloutTestAccount(1002, 50)
	foundDifferentBranch := false
	for index := range 1000 {
		ctx := withModelMappingRolloutPin(modelMappingRolloutTestContext(fmt.Sprintf("websocket-account-independent-%d", index)))
		firstModel, _ := first.ResolveMappedModelForRequest(ctx, "model-a")
		secondModel, _ := second.ResolveMappedModelForRequest(ctx, "model-a")
		if firstModel != secondModel {
			foundDifferentBranch = true
			break
		}
	}
	require.True(t, foundDifferentBranch)
}

func TestShouldApplyModelMappingPercentBucketBoundaries(t *testing.T) {
	require.False(t, shouldApplyModelMappingPercent(0, 0))
	require.True(t, shouldApplyModelMappingPercent(100, 9999))
	require.True(t, shouldApplyModelMappingPercent(25, 2499))
	require.False(t, shouldApplyModelMappingPercent(25, 2500))
}

func TestAccountMappingUsageProvenanceMatchesSelectedBranch(t *testing.T) {
	ctx := modelMappingRolloutTestContext("usage-provenance")

	mappedAccount := modelMappingRolloutTestAccount(1, 100)
	mappedResult := &OpenAIForwardResult{}
	applyOpenAIForwardResultAccountMapping(ctx, mappedResult, mappedAccount, "model-a")
	require.True(t, mappedResult.AccountMappingApplied)
	require.Equal(t, "model-a", mappedResult.AccountMappingSourceModel)
	require.Equal(t, "mapped-a", mappedResult.AccountMappingTargetModel)

	bypassedAccount := modelMappingRolloutTestAccount(1, 0)
	bypassedResult := &OpenAIForwardResult{}
	applyOpenAIForwardResultAccountMapping(ctx, bypassedResult, bypassedAccount, "model-a")
	require.False(t, bypassedResult.AccountMappingApplied)
	require.Empty(t, bypassedResult.AccountMappingSourceModel)
	require.Empty(t, bypassedResult.AccountMappingTargetModel)
}

func TestAccountResolveMappedModelForRequestDoesNotAllocateAfterWarmup(t *testing.T) {
	ctx := modelMappingRolloutTestContext("allocation-check")
	for _, percent := range []int{100, 37} {
		account := modelMappingRolloutTestAccount(55, percent)
		_, _ = account.ResolveMappedModelForRequest(ctx, "model-a")
		allocations := testing.AllocsPerRun(1000, func() {
			_, _ = account.ResolveMappedModelForRequest(ctx, "model-a")
		})
		require.Zero(t, allocations, "percent=%d", percent)
	}
}

func BenchmarkAccountResolveMappedModelForRequest(b *testing.B) {
	ctx := modelMappingRolloutTestContext("benchmark-request")
	for _, percent := range []int{100, 37} {
		b.Run(fmt.Sprintf("percent_%d", percent), func(b *testing.B) {
			account := modelMappingRolloutTestAccount(55, percent)
			_, _ = account.ResolveMappedModelForRequest(ctx, "model-a")
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_, _ = account.ResolveMappedModelForRequest(ctx, "model-a")
			}
		})
	}
}
