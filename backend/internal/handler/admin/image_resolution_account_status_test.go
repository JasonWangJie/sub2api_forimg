package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type resolutionAccountStatusRepo struct {
	service.ImageResolutionGuardRepository
	ids   [][]int64
	items []service.ImageResolutionCooldown
	err   error
}

func (r *resolutionAccountStatusRepo) ListCooldownsBatch(_ context.Context, ids []int64) ([]service.ImageResolutionCooldown, error) {
	r.ids = append(r.ids, append([]int64{}, ids...))
	return r.items, r.err
}

func TestImageResolutionAccountStatusListAndRefresh(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminSvc := newStubAdminService()
	adminSvc.accounts = []service.Account{{ID: 12, Name: "channel-a", Platform: service.PlatformOpenAI}, {ID: 13, Name: "channel-b", Platform: service.PlatformGemini}}
	repo := &resolutionAccountStatusRepo{items: []service.ImageResolutionCooldown{
		{AccountID: 12, Model: "gpt-image-2", SizeTier: "4K", CycleID: "cycle-a", CooldownUntil: time.Now().UTC().Add(time.Minute)},
		{AccountID: 12, Model: "other-model", SizeTier: "2K", CycleID: "cycle-b", CooldownUntil: time.Now().UTC().Add(time.Minute)},
	}}
	settings := &resolutionHandlerSettings{value: `{"enabled":true}`}
	guard := service.NewImageResolutionGuardService(repo, settings, nil, nil, nil)
	h := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetImageResolutionGuardService(guard)
	router := gin.New()
	router.GET("/accounts", h.List)
	do := func(etag string) (*httptest.ResponseRecorder, []AccountWithConcurrency) {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/accounts?lite=true", nil)
		req.Header.Set("If-None-Match", etag)
		router.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, w.Body.String())
		var response struct {
			Data struct {
				Items []AccountWithConcurrency `json:"items"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		return w, response.Data.Items
	}
	w, items := do("")
	require.Equal(t, [][]int64{{12, 13}}, repo.ids, "one page must use one batch call")
	require.Len(t, items, 2)
	require.True(t, items[0].ImageResolutionGuard.Enabled)
	require.Len(t, items[0].ImageResolutionGuard.Cooldowns, 2)
	require.NotNil(t, items[1].ImageResolutionGuard.Cooldowns)
	require.Empty(t, items[1].ImageResolutionGuard.Cooldowns)
	oldETag := w.Header().Get("ETag")
	require.NotEmpty(t, oldETag)

	// Clearing must invalidate the list ETag even when the account row did not change.
	repo.items = nil
	w, items = do(oldETag)
	require.NotEqual(t, oldETag, w.Header().Get("ETag"))
	require.Empty(t, items[0].ImageResolutionGuard.Cooldowns)

	// Unavailable is omitted, never synthesized as a confirmed recovery.
	repo.err = errors.New("database unavailable")
	_, items = do("")
	require.Nil(t, items[0].ImageResolutionGuard)

	repo.err = nil
	repo.items = []service.ImageResolutionCooldown{{AccountID: 12, Model: "gpt-image-2", SizeTier: "4K", CooldownUntil: time.Now().Add(time.Minute)}}
	cfg := service.DefaultImageResolutionGuardSettings()
	_, err := guard.UpdateSettings(context.Background(), &cfg)
	require.NoError(t, err)
	_, items = do("")
	require.False(t, items[0].ImageResolutionGuard.Enabled)
	require.Len(t, items[0].ImageResolutionGuard.Cooldowns, 1, "disabled enforcement retains its record")
	detail := h.buildAccountResponseWithRuntime(context.Background(), &adminSvc.accounts[0])
	require.NotNil(t, detail.ImageResolutionGuard)
	require.Len(t, detail.ImageResolutionGuard.Cooldowns, 1)

	statuses, err := guard.AccountStatuses(context.Background(), []int64{12, 12, 0, -1, 13})
	require.NoError(t, err)
	require.Len(t, statuses, 2)
	require.Equal(t, []int64{12, 13}, repo.ids[len(repo.ids)-1])
}
