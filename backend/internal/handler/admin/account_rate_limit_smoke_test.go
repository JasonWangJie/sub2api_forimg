package admin

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// These smoke tests use the real JWT middleware, HTTP handler, services and
// repository SQL. Only database transport and runtime caches are test doubles.
type rateLimitSmokeUsers struct {
	service.UserRepository
}

func (*rateLimitSmokeUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	role := service.RoleAdmin
	if id == 2 {
		role = service.RoleUser
	}
	return &service.User{ID: id, Email: "smoke@example.test", Role: role, Status: service.StatusActive, TokenVersion: 1, TokenVersionResolved: true}, nil
}

func (*rateLimitSmokeUsers) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

type rateLimitSmokeRuntime struct {
	service.TempUnschedCache
	deleted   []int64
	cleared   []int64
	deleteErr error
	audit     map[string]any
}

func (r *rateLimitSmokeRuntime) DeleteTempUnsched(_ context.Context, id int64) error {
	r.deleted = append(r.deleted, id)
	return r.deleteErr
}

func (r *rateLimitSmokeRuntime) BlockAccountScheduling(*service.Account, time.Time, string) {}
func (r *rateLimitSmokeRuntime) ClearAccountSchedulingBlock(id int64) {
	r.cleared = append(r.cleared, id)
}

func setupRateLimitSmoke(t *testing.T, auditHandlers ...gin.HandlerFunc) (*gin.Engine, sqlmock.Sqlmock, *rateLimitSmokeRuntime, func(int64) string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()) })
	repo := repository.NewAccountRepository(client, db, nil)
	adminSvc := service.NewAdminService(nil, nil, repo.(service.AdminAccountRepository), nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, client, nil, nil, nil, nil, nil, nil, nil, nil)
	runtime := &rateLimitSmokeRuntime{}
	limitSvc := service.NewRateLimitService(repo, nil, nil, nil, runtime)
	limitSvc.SetAccountRuntimeBlocker(runtime)
	h := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, limitSvc, nil, nil, nil, nil, nil, nil, nil)
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "local-rate-limit-smoke-only", ExpireHour: 1}}
	auth := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	users := service.NewUserService(&rateLimitSmokeUsers{}, nil, nil, nil)
	router := gin.New()
	group := router.Group("/api/v1/admin", gin.HandlerFunc(middleware.NewAdminAuthMiddleware(auth, users, nil, nil)))
	group.Use(auditHandlers...)
	group.Use(func(c *gin.Context) {
		c.Next()
		if value, ok := c.Get("audit_extra"); ok {
			runtime.audit, _ = value.(map[string]any)
		}
	})
	group.POST("/accounts/:id/clear-rate-limit", h.ClearRateLimit)
	group.POST("/accounts/:id/recover-state", h.RecoverState)
	group.DELETE("/accounts/:id/temp-unschedulable", h.ClearTempUnschedulable)
	group.GET("/accounts/:id/rate-limit-state", h.GetRateLimitState)
	token := func(id int64) string {
		role := service.RoleAdmin
		if id == 2 {
			role = service.RoleUser
		}
		value, tokenErr := auth.GenerateToken(context.Background(), &service.User{ID: id, Role: role, TokenVersion: 1, TokenVersionResolved: true})
		require.NoError(t, tokenErr)
		return value
	}
	return router, mock, runtime, token
}

func expectRateLimitSmokeOutbox(mock sqlmock.Sqlmock) {
	mock.ExpectExec("INSERT INTO scheduler_outbox").
		WithArgs(service.SchedulerOutboxEventAccountChanged, int64(42), nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
}

func smokeRateLimitState() *service.AccountRateLimitState {
	until := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	return &service.AccountRateLimitState{AccountID: 42, Status: "error", Schedulable: false, RateLimitResetAt: &until}
}
func smokeRateLimitRows(empty bool) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"id", "status", "schedulable", "rate_limited_at", "rate_limit_reset_at", "overload_until", "temp_unschedulable_until", "temp_unschedulable_reason", "model_rate_limits", "quota_scopes"})
	if !empty {
		rows.AddRow(42, "error", false, nil, *smokeRateLimitState().RateLimitResetAt, nil, nil, nil, []byte("null"), []byte("null"))
	}
	return rows
}
func expectRateLimitSmokeClear(mock sqlmock.Sqlmock, failOutbox bool) {
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,status,schedulable").WithArgs(int64(42)).WillReturnRows(smokeRateLimitRows(false))
	mock.ExpectExec("UPDATE accounts SET rate_limited_at=NULL").WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	if failOutbox {
		mock.ExpectExec("INSERT INTO scheduler_outbox").WillReturnError(errors.New("injected outbox failure"))
		mock.ExpectRollback()
		return
	}
	expectRateLimitSmokeOutbox(mock)
	mock.ExpectCommit()
}

func expectRateLimitSmokeRead(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT .* FROM "accounts"`).WillReturnRows(sqlmock.NewRows([]string{
		"id", "name", "platform", "type", "status", "schedulable", "credentials", "extra", "rate_limit_reset_at", "overload_until", "temp_unschedulable_until",
	}).AddRow(42, "manual smoke", "gemini", "apikey", "error", false, []byte(`{}`), []byte(`{}`), nil, nil, nil))
	mock.ExpectQuery(`SELECT .* FROM "account_groups"`).WillReturnRows(sqlmock.NewRows([]string{"account_id", "group_id", "priority"}))
}

func callRateLimitSmoke(router *gin.Engine, id, token string) *httptest.ResponseRecorder {
	return callRateLimitSmokeRequest(router, http.MethodPost, "/accounts/"+id+"/clear-rate-limit", token, `{"expected_version":"`+smokeRateLimitState().Version()+`"}`)
}
func callRateLimitSmokeRequest(router *gin.Engine, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/v1/admin"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestAccountRateLimitSmoke(t *testing.T) {
	t.Run("admin_only_and_invalid_id_do_not_write", func(t *testing.T) {
		router, _, runtime, token := setupRateLimitSmoke(t)
		require.Equal(t, http.StatusUnauthorized, callRateLimitSmoke(router, "42", "").Code)
		require.Equal(t, http.StatusForbidden, callRateLimitSmoke(router, "42", token(2)).Code)
		require.Equal(t, http.StatusBadRequest, callRateLimitSmoke(router, "invalid", token(1)).Code)
		require.Empty(t, runtime.cleared)
	})
	t.Run("success_preserves_disabled_and_error_state", func(t *testing.T) {
		router, mock, runtime, token := setupRateLimitSmoke(t)
		expectRateLimitSmokeClear(mock, false)
		expectRateLimitSmokeRead(mock)
		rec := callRateLimitSmoke(router, "42", token(1))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var payload struct {
			Data struct {
				Account struct {
					Status                 string  `json:"status"`
					Schedulable            bool    `json:"schedulable"`
					RateLimitResetAt       *string `json:"rate_limit_reset_at"`
					OverloadUntil          *string `json:"overload_until"`
					TempUnschedulableUntil *string `json:"temp_unschedulable_until"`
				} `json:"account"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
		require.False(t, payload.Data.Account.Schedulable)
		require.Equal(t, "error", payload.Data.Account.Status)
		require.Nil(t, payload.Data.Account.RateLimitResetAt)
		require.Nil(t, payload.Data.Account.OverloadUntil)
		require.Nil(t, payload.Data.Account.TempUnschedulableUntil)
		require.Equal(t, []int64{42}, runtime.deleted)
		require.Equal(t, []int64{42}, runtime.cleared)
	})
	t.Run("missing_account_is_404", func(t *testing.T) {
		router, mock, runtime, token := setupRateLimitSmoke(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id,status,schedulable").WithArgs(int64(42)).WillReturnRows(smokeRateLimitRows(true))
		mock.ExpectRollback()
		rec := callRateLimitSmoke(router, "42", token(1))
		require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		require.Empty(t, runtime.cleared)
	})
	t.Run("outbox_failure_rolls_back_and_retry_succeeds", func(t *testing.T) {
		router, mock, runtime, token := setupRateLimitSmoke(t)
		expectRateLimitSmokeClear(mock, true)
		first := callRateLimitSmoke(router, "42", token(1))
		require.Equal(t, http.StatusInternalServerError, first.Code, first.Body.String())
		require.Empty(t, runtime.deleted)
		require.Empty(t, runtime.cleared)
		// The transaction rolled back every field. The same preview remains valid.
		expectRateLimitSmokeClear(mock, false)
		expectRateLimitSmokeRead(mock)
		second := callRateLimitSmoke(router, "42", token(1))
		require.Equal(t, http.StatusOK, second.Code, second.Body.String())
		require.Equal(t, []int64{42}, runtime.cleared)
	})
}

func TestAccountRateLimitPreviewGuardSmoke(t *testing.T) {
	t.Run("missing_preview_and_old_temp_delete_cannot_write", func(t *testing.T) {
		router, _, runtime, token := setupRateLimitSmoke(t)
		require.Equal(t, 400, callRateLimitSmokeRequest(router, "POST", "/accounts/42/clear-rate-limit", token(1), "").Code)
		rec := callRateLimitSmokeRequest(router, "DELETE", "/accounts/42/temp-unschedulable", token(1), "")
		require.Equal(t, 409, rec.Code)
		require.Contains(t, rec.Body.String(), "RATE_LIMIT_PREVIEW_REQUIRED")
		require.Empty(t, runtime.cleared)
	})
	t.Run("stale_preview_conflict_leaves_state_untouched", func(t *testing.T) {
		router, mock, runtime, token := setupRateLimitSmoke(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id,status,schedulable").WillReturnRows(smokeRateLimitRows(false))
		mock.ExpectRollback()
		rec := callRateLimitSmokeRequest(router, "POST", "/accounts/42/clear-rate-limit", token(1), `{"expected_version":"`+strings.Repeat("0", 64)+`"}`)
		require.Equal(t, 409, rec.Code)
		require.Contains(t, rec.Body.String(), "RATE_LIMIT_STATE_CHANGED")
		require.Empty(t, runtime.cleared)
		require.Equal(t, "not_committed", runtime.audit["result"])
	})
	t.Run("preview_is_read_only_and_legacy_recovery_rejects_active_limits", func(t *testing.T) {
		router, mock, runtime, token := setupRateLimitSmoke(t)
		mock.ExpectQuery("SELECT id,status,schedulable").WillReturnRows(smokeRateLimitRows(false))
		preview := callRateLimitSmokeRequest(router, "GET", "/accounts/42/rate-limit-state", token(1), "")
		require.Equal(t, 200, preview.Code)
		require.Contains(t, preview.Body.String(), smokeRateLimitState().Version())
		require.NotContains(t, preview.Body.String(), "credentials")
		mock.ExpectQuery("SELECT id,status,schedulable").WillReturnRows(smokeRateLimitRows(false))
		rec := callRateLimitSmokeRequest(router, "POST", "/accounts/42/recover-state", token(1), "")
		require.Equal(t, 409, rec.Code)
		require.Contains(t, rec.Body.String(), "RATE_LIMIT_PREVIEW_REQUIRED")
		require.Empty(t, runtime.cleared)
	})
	t.Run("cache_and_account_refresh_failure_are_committed_warnings", func(t *testing.T) {
		router, mock, runtime, token := setupRateLimitSmoke(t)
		runtime.deleteErr = errors.New("injected cache failure")
		expectRateLimitSmokeClear(mock, false)
		mock.ExpectQuery(`SELECT .* FROM "accounts"`).WillReturnError(errors.New("read unavailable"))
		rec := callRateLimitSmoke(router, "42", token(1))
		require.Equal(t, 200, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), `"account":null`)
		require.Contains(t, rec.Body.String(), `"status":"warning"`)
		require.Contains(t, rec.Body.String(), "account_refresh")
		require.Contains(t, rec.Body.String(), "temp_unsched")
		require.Equal(t, "committed", runtime.audit["result"])
		require.Equal(t, "warning", runtime.audit["rate_limit_cache_sync"])
		require.Equal(t, 1, runtime.audit["rate_limit_before_count"])
		require.Equal(t, 0, runtime.audit["rate_limit_after_count"])
		require.NotEmpty(t, runtime.audit["rate_limit_before_account_until"])
	})
}

func (r *rateLimitSmokeRuntime) SnapshotTempUnsched(context.Context, int64) (string, error) {
	return "snapshot", nil
}
func (r *rateLimitSmokeRuntime) DeleteTempUnschedIfObserved(ctx context.Context, id int64, _ string) (bool, error) {
	err := r.DeleteTempUnsched(ctx, id)
	return err == nil, err
}
func (r *rateLimitSmokeRuntime) SnapshotAccountSchedulingBlock(int64) (uint64, time.Time) {
	return 0, time.Time{}
}
func (r *rateLimitSmokeRuntime) ClearAccountSchedulingBlockIfObserved(id int64, _ uint64) bool {
	r.ClearAccountSchedulingBlock(id)
	return true
}

type rateLimitSmokeSQLCapture struct{ value driver.Value }

func (c *rateLimitSmokeSQLCapture) Match(value driver.Value) bool {
	c.value = value
	return true
}

// Exercise the complete audit handoff, including asynchronous flushing and the
// production COPY repository. SQL transport remains mocked; this does not claim
// durability in a live PostgreSQL database.
func TestAccountRateLimitAuditPipelineSmoke(t *testing.T) {
	for _, committed := range []bool{true, false} {
		name := "stale_preview_not_committed"
		if committed {
			name = "cache_warning_still_committed"
		}
		t.Run(name, func(t *testing.T) {
			auditDB, auditMock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = auditDB.Close() })
			auditSvc := service.NewAuditLogService(repository.NewAuditLogRepository(auditDB), nil)
			auditSvc.Start()
			t.Cleanup(auditSvc.Stop)
			router, mock, runtime, token := setupRateLimitSmoke(t, gin.HandlerFunc(middleware.NewAuditLogMiddleware(auditSvc)))
			jwt := token(1)
			status, version := http.StatusConflict, strings.Repeat("0", 64)
			if committed {
				status, version = http.StatusOK, smokeRateLimitState().Version()
				runtime.deleteErr = errors.New("injected cache failure")
				expectRateLimitSmokeClear(mock, false)
				mock.ExpectQuery(`SELECT .* FROM "accounts"`).WillReturnError(errors.New("read unavailable"))
			} else {
				mock.ExpectBegin()
				mock.ExpectQuery("SELECT id,status,schedulable").WillReturnRows(smokeRateLimitRows(false))
				mock.ExpectRollback()
			}
			credential, body, extra := &rateLimitSmokeSQLCapture{}, &rateLimitSmokeSQLCapture{}, &rateLimitSmokeSQLCapture{}
			auditMock.ExpectBegin()
			copyStmt := auditMock.ExpectPrepare(`COPY "audit_logs"`)
			copyStmt.ExpectExec().WithArgs(
				sqlmock.AnyArg(), int64(1), "smoke@example.test", service.RoleAdmin, service.AuditAuthMethodJWT,
				credential, "admin.accounts.clear_rate_limit", http.MethodPost, "/api/v1/admin/accounts/:id/clear-rate-limit",
				"", sqlmock.AnyArg(), "", body, status, sqlmock.AnyArg(), extra,
			).WillReturnResult(sqlmock.NewResult(0, 1))
			copyStmt.ExpectExec().WithArgs().WillReturnResult(sqlmock.NewResult(0, 0))
			auditMock.ExpectCommit()
			response := callRateLimitSmokeRequest(router, http.MethodPost, "/accounts/42/clear-rate-limit", jwt,
				`{"expected_version":"`+version+`","password":"audit-secret-canary"}`)
			require.Equal(t, status, response.Code, response.Body.String())
			// Stop waits for the writer to drain; there are no polling sleeps or
			// unsynchronized reads of arguments captured by the writer goroutine.
			auditSvc.Stop()
			require.NoError(t, auditMock.ExpectationsWereMet())
			require.NotContains(t, credential.value, jwt)
			require.NotContains(t, body.value, "audit-secret-canary")
			var details map[string]any
			require.NoError(t, json.Unmarshal([]byte(extra.value.(string)), &details))
			require.Equal(t, map[string]any{"id": "42"}, details["params"])
			if committed {
				require.Equal(t, "committed", details["result"])
				require.Equal(t, "warning", details["rate_limit_cache_sync"])
				require.Contains(t, details["rate_limit_cache_failed"], "temp_unsched")
				require.Contains(t, details["rate_limit_cache_failed"], "account_refresh")
				require.Equal(t, smokeRateLimitState().Version(), details["rate_limit_before_version"])
				require.NotEmpty(t, details["rate_limit_after_version"])
				require.EqualValues(t, 1, details["rate_limit_before_count"])
				require.EqualValues(t, 0, details["rate_limit_after_count"])
				require.Equal(t, false, details["rate_limit_schedulable"])
				require.Equal(t, "error", details["rate_limit_account_status"])
			} else {
				require.Equal(t, "not_committed", details["result"])
				require.Equal(t, "RATE_LIMIT_STATE_CHANGED", details["error_code"])
			}
		})
	}
}
