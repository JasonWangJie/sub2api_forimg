package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

func optionalTrimmedStringPtr(raw string) *string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func forwardResultBillingModel(requestedModel, upstreamModel string) string {
	if trimmed := strings.TrimSpace(requestedModel); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(upstreamModel)
}

type usageBillingModelSelection struct {
	Primary                 string
	AccountMappingFallback  string
	AccountMappingPreferred bool
}

func selectUsageBillingModel(
	billingModelSource string,
	originalModel string,
	channelMappedModel string,
	upstreamModel string,
	currentDefault string,
	accountMappingApplied bool,
	accountMappingSource string,
	accountMappingTarget string,
) usageBillingModelSelection {
	originalModel = strings.TrimSpace(originalModel)
	channelMappedModel = strings.TrimSpace(channelMappedModel)
	upstreamModel = strings.TrimSpace(upstreamModel)
	currentDefault = strings.TrimSpace(currentDefault)
	accountMappingSource = strings.TrimSpace(accountMappingSource)
	accountMappingTarget = strings.TrimSpace(accountMappingTarget)

	primary := currentDefault
	switch billingModelSource {
	case BillingModelSourceRequested:
		if originalModel != "" {
			primary = originalModel
		}
	case BillingModelSourceChannelMapped:
		if channelMappedModel != "" {
			primary = channelMappedModel
		}
	case BillingModelSourceUpstream:
		if accountMappingApplied && accountMappingSource != "" {
			primary = accountMappingSource
		} else if upstreamModel != "" {
			primary = upstreamModel
		}
	default:
		if accountMappingApplied && accountMappingSource != "" {
			primary = accountMappingSource
		}
	}

	accountMappingPreferred := accountMappingApplied &&
		accountMappingSource != "" &&
		strings.EqualFold(strings.TrimSpace(primary), accountMappingSource)
	selection := usageBillingModelSelection{
		Primary:                 primary,
		AccountMappingPreferred: accountMappingPreferred,
	}
	if accountMappingPreferred && accountMappingTarget != "" &&
		!strings.EqualFold(accountMappingSource, accountMappingTarget) {
		selection.AccountMappingFallback = accountMappingTarget
	}
	return selection
}

func accountMappingResolutionForUsage(ctx context.Context, account *Account, sourceModel string) AccountModelMappingResolution {
	if account == nil {
		return AccountModelMappingResolution{Model: sourceModel, InputModel: strings.TrimSpace(sourceModel)}
	}
	return account.ResolveMappedModelDetailedForRequest(ctx, sourceModel)
}

func applyForwardResultAccountMapping(ctx context.Context, result *ForwardResult, account *Account, sourceModel string) {
	if result == nil || result.AccountMappingApplied {
		return
	}
	resolution := accountMappingResolutionForUsage(ctx, account, sourceModel)
	if !resolution.ExplicitChanged {
		return
	}
	result.AccountMappingApplied = true
	result.AccountMappingSourceModel = resolution.InputModel
	result.AccountMappingTargetModel = resolution.ExplicitTarget
}

func applyOpenAIForwardResultAccountMapping(ctx context.Context, result *OpenAIForwardResult, account *Account, sourceModel string) {
	if result == nil || result.AccountMappingApplied {
		return
	}
	resolution := accountMappingResolutionForUsage(ctx, account, sourceModel)
	if !resolution.ExplicitChanged {
		return
	}
	result.AccountMappingApplied = true
	result.AccountMappingSourceModel = resolution.InputModel
	result.AccountMappingTargetModel = resolution.ExplicitTarget
}

func sameUsageBillingModelFamily(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return left == right
	}
	for _, candidate := range usageBillingModelCandidates(right) {
		if strings.EqualFold(left, candidate) {
			return true
		}
	}
	return false
}

func modelHasSpecificImagePrice(billingService *BillingService, model string) bool {
	if billingService == nil {
		return false
	}
	if _, ok := getDefaultGrokImagineImagePrice(model, ImageBillingSize1K); ok {
		return true
	}
	if billingService.pricingService == nil {
		return false
	}
	pricing := billingService.pricingService.GetModelPricing(model)
	return pricing != nil && pricing.OutputCostPerImage > 0
}

func modelHasSpecificVideoPrice(billingService *BillingService, model string) bool {
	if billingService == nil {
		return false
	}
	if _, ok := getDefaultGrokImagineVideoPrice(model, VideoBillingResolution720P); ok {
		return true
	}
	return modelHasSpecificImagePrice(billingService, model)
}

func billingModelsWithPreferredFirst(candidates []string, preferred string) []string {
	preferred = strings.TrimSpace(preferred)
	if preferred == "" || len(candidates) < 2 || strings.EqualFold(strings.TrimSpace(candidates[0]), preferred) {
		return candidates
	}
	reordered := make([]string, 0, len(candidates))
	reordered = append(reordered, preferred)
	for _, candidate := range candidates {
		if !strings.EqualFold(strings.TrimSpace(candidate), preferred) {
			reordered = append(reordered, candidate)
		}
	}
	return reordered
}

func logAccountMappingPricingFallback(
	component string,
	selectedModel string,
	requestedModel string,
	mappingSourceModel string,
	mappingTargetModel string,
	accountID int64,
	apiKeyID int64,
	channelID int64,
) {
	logger.L().With(
		zap.String("component", component),
		zap.String("requested_model", strings.TrimSpace(requestedModel)),
		zap.String("account_mapping_source_model", strings.TrimSpace(mappingSourceModel)),
		zap.String("account_mapping_target_model", strings.TrimSpace(mappingTargetModel)),
		zap.String("selected_billing_model", strings.TrimSpace(selectedModel)),
		zap.Int64("account_id", accountID),
		zap.Int64("api_key_id", apiKeyID),
		zap.Int64("channel_id", channelID),
	).Warn("usage.account_mapping_source_pricing_unavailable_fallback")
}

func optionalInt64Ptr(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}
