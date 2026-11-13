package admin

import (
	"context"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (h *AccountHandler) imageResolutionAccountStatuses(ctx context.Context, ids []int64) map[int64]*service.ImageResolutionAccountStatus {
	if h.imageResolutionGuard == nil || len(ids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	statuses, err := h.imageResolutionGuard.AccountStatuses(ctx, ids)
	if err != nil {
		slog.Warn("image_resolution.account_status_failed", "error", err)
		return nil
	}
	return statuses
}
