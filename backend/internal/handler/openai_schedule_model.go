package handler

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func openAIAccountScheduleModel(ctx context.Context, account *service.Account, requestedModel string, result *service.OpenAIForwardResult) string {
	if result != nil {
		if upstreamModel := strings.TrimSpace(result.UpstreamModel); upstreamModel != "" {
			return upstreamModel
		}
	}
	if account == nil {
		return strings.TrimSpace(requestedModel)
	}
	return account.GetMappedModelForRequest(ctx, requestedModel)
}
