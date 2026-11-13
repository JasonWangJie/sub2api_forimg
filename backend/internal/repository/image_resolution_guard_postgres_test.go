//go:build postgres

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func imageResolutionPostgresDB(t *testing.T) *sql.DB {
	t.Helper()
	control := openAsyncImagePostgresTestDB(t)
	schema := "resolution_guard_" + fmt.Sprint(time.Now().UnixNano())
	_, err := control.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := control.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`)
		require.NoError(t, err)
	})
	u, err := url.Parse(os.Getenv("SUB2API_TEST_POSTGRES_DSN"))
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`CREATE TABLE accounts(id BIGINT PRIMARY KEY); INSERT INTO accounts VALUES(12),(13)`)
	require.NoError(t, err)
	data, err := migrations.FS.ReadFile("231_ZJ_image_resolution_guard.sql")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = db.Exec(string(data))
		require.NoError(t, err)
	}
	return db
}

func imageResolutionPGRecord(model string, index int, outcome string) service.ImageResolutionRecord {
	cfg := service.DefaultImageResolutionGuardSettings()
	cfg.Enabled = true
	cfg.FailureThreshold = 2
	now := time.Now().UTC()
	return service.ImageResolutionRecord{Observation: service.ImageResolutionObservation{ID: fmt.Sprintf("%s-%d", model, index), AccountID: 12, AccountName: "test account", RequestID: "req", StartedAt: now, Expectation: service.ImageResolutionExpectation{RequestedModel: model, UpstreamModel: model, SizeTier: "4K", ExpectedWidth: 2880, ExpectedHeight: 2880, ExpectedPixels: 2880 * 2880, RuleVersion: 1}, Outputs: []service.ImageResolutionOutput{{Width: 1024, Height: 1024}}}, Outcome: outcome, Settings: cfg, Recipients: []string{"one@example.com", "two@example.com"}, Now: now}
}

func TestImageResolutionGuardPostgresLifecycle(t *testing.T) {
	db := imageResolutionPostgresDB(t)
	repo := NewImageResolutionGuardRepository(db)
	ctx := context.Background()
	r1 := imageResolutionPGRecord("lifecycle", 1, "mismatch")
	opened, err := repo.Record(ctx, r1)
	require.NoError(t, err)
	require.Nil(t, opened)
	_, err = repo.Record(ctx, r1)
	require.NoError(t, err)
	var count int
	require.NoError(t, db.QueryRow(`SELECT failure_count FROM image_resolution_quality_state WHERE requested_model='lifecycle'`).Scan(&count))
	require.Equal(t, 1, count)
	unknown := imageResolutionPGRecord("lifecycle", 2, "unknown")
	_, err = repo.Record(ctx, unknown)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT failure_count FROM image_resolution_quality_state WHERE requested_model='lifecycle'`).Scan(&count))
	require.Equal(t, 1, count)
	pass := imageResolutionPGRecord("lifecycle", 3, "pass")
	_, err = repo.Record(ctx, pass)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT failure_count FROM image_resolution_quality_state WHERE requested_model='lifecycle'`).Scan(&count))
	require.Zero(t, count)
	r4 := imageResolutionPGRecord("lifecycle", 4, "mismatch")
	_, err = repo.Record(ctx, r4)
	require.NoError(t, err)
	r5 := imageResolutionPGRecord("lifecycle", 5, "mismatch")
	opened, err = repo.Record(ctx, r5)
	require.NoError(t, err)
	require.NotNil(t, opened)
	deadline := opened.CooldownUntil
	cycle := opened.CycleID
	require.Equal(t, 1024, opened.ActualWidth)
	require.InDelta(t, 12.64, opened.AreaPercent, .01)
	late := imageResolutionPGRecord("lifecycle", 6, "pass")
	_, err = repo.Record(ctx, late)
	require.NoError(t, err)
	stored, err := repo.GetCooldown(ctx, 12, "lifecycle", "4K")
	require.NoError(t, err)
	require.WithinDuration(t, deadline, stored.CooldownUntil, time.Microsecond)
	other, err := repo.GetCooldown(ctx, 13, "lifecycle", "4K")
	require.NoError(t, err)
	require.Nil(t, other)
	other, err = repo.GetCooldown(ctx, 12, "lifecycle", "2K")
	require.NoError(t, err)
	require.Nil(t, other)
	// A reconstructed repository sees the durable cooling state.
	restarted := NewImageResolutionGuardRepository(db)
	stored, err = restarted.GetCooldown(ctx, 12, "lifecycle", "4K")
	require.NoError(t, err)
	require.Equal(t, cycle, stored.CycleID)
	items, err := repo.ListCooldowns(ctx, 12)
	require.NoError(t, err)
	require.Len(t, items, 1)
	cleared := time.Now().UTC()
	require.NoError(t, repo.ClearCooldown(ctx, 12, cycle, cleared))
	require.NoError(t, repo.ClearCooldown(ctx, 12, cycle, cleared))
	stored, err = repo.GetCooldown(ctx, 12, "lifecycle", "4K")
	require.NoError(t, err)
	require.Nil(t, stored)
	late.Outcome = "mismatch"
	late.Observation.ID = "late-after-clear"
	late.Observation.StartedAt = cleared.Add(-time.Second)
	_, err = repo.Record(ctx, late)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT failure_count FROM image_resolution_quality_state WHERE requested_model='lifecycle'`).Scan(&count))
	require.Zero(t, count)
}

func TestImageResolutionGuardPostgresConcurrentAndMailRecovery(t *testing.T) {
	db := imageResolutionPostgresDB(t)
	repo := NewImageResolutionGuardRepository(db)
	ctx := context.Background()
	start := time.Now().UTC()
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			v := imageResolutionPGRecord("concurrent", index, "mismatch")
			v.Settings.FailureThreshold = 3
			v.Observation.StartedAt = start
			_, err := repo.Record(ctx, v)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var events int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM image_resolution_quality_events WHERE cycle_id<>''`).Scan(&events))
	require.Equal(t, 1, events)
	var failures int
	require.NoError(t, db.QueryRow(`SELECT failure_count FROM image_resolution_quality_state`).Scan(&failures))
	require.Equal(t, 3, failures)
	claimed := make(chan []service.ImageResolutionMailEvent, 6)
	errs2 := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			events, err := repo.ClaimMail(ctx, uuid.NewString(), 1, 2*time.Minute)
			claimed <- events
			errs2 <- err
		}()
	}
	wg.Wait()
	close(claimed)
	close(errs2)
	for err := range errs2 {
		require.NoError(t, err)
	}
	var first service.ImageResolutionMailEvent
	total := 0
	for result := range claimed {
		total += len(result)
		if len(result) > 0 {
			first = result[0]
		}
	}
	require.Equal(t, 1, total)
	require.Equal(t, 1, first.Attempts)
	require.NoError(t, repo.SaveMailProgress(ctx, first.ID, first.ClaimToken, []string{"one@example.com"}))
	_, err := db.Exec(`UPDATE image_resolution_quality_events SET claimed_at=NOW()-INTERVAL '3 minutes' WHERE id=$1`, first.ID)
	require.NoError(t, err)
	restarted := NewImageResolutionGuardRepository(db)
	second, err := restarted.ClaimMail(ctx, "restarted-worker", 1, 2*time.Minute)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, 2, second[0].Attempts)
	require.Equal(t, []string{"one@example.com"}, second[0].Delivered)
	require.Error(t, repo.SaveMailProgress(ctx, first.ID, first.ClaimToken, nil))
	require.Error(t, repo.FinishMail(ctx, first.ID, first.ClaimToken, "sent", nil, time.Now(), ""))
	require.NoError(t, repo.FinishMail(ctx, first.ID, "restarted-worker", "pending", second[0].Delivered, time.Now().Add(-time.Second), "mail_delivery_failed"))
	third, err := repo.ClaimMail(ctx, "final-worker", 1, 2*time.Minute)
	require.NoError(t, err)
	require.Len(t, third, 1)
	require.Equal(t, 3, third[0].Attempts)
	require.NoError(t, repo.FinishMail(ctx, first.ID, "final-worker", "sent", []string{"one@example.com", "two@example.com"}, time.Now(), ""))
	none, err := repo.ClaimMail(ctx, "no-more", 1, 2*time.Minute)
	require.NoError(t, err)
	require.Empty(t, none)
}

func TestImageResolutionGuardPostgresRetentionProtectsPendingAndClaimed(t *testing.T) {
	db := imageResolutionPostgresDB(t)
	repo := NewImageResolutionGuardRepository(db)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		v := imageResolutionPGRecord(fmt.Sprintf("retention%d", i), i, "unknown")
		_, err := repo.Record(ctx, v)
		require.NoError(t, err)
	}
	_, err := db.Exec(`UPDATE image_resolution_quality_events SET created_at=NOW()-INTERVAL '10 days'; UPDATE image_resolution_quality_events SET mail_status='pending' WHERE observation_id='retention1-1'; UPDATE image_resolution_quality_events SET claim_token='active' WHERE observation_id='retention2-2'; UPDATE image_resolution_quality_events SET mail_status='failed' WHERE observation_id='retention3-3'`)
	require.NoError(t, err)
	maintenance := repo.(service.ImageResolutionGuardMaintenanceRepository)
	n, err := maintenance.Prune(ctx, time.Now().Add(-7*24*time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
	var remain int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM image_resolution_quality_events`).Scan(&remain))
	require.Equal(t, 2, remain)
}

func TestImageResolutionGuardPostgresExpiryAndStaleCompletion(t *testing.T) {
	db := imageResolutionPostgresDB(t)
	repo := NewImageResolutionGuardRepository(db)
	ctx := context.Background()
	v := imageResolutionPGRecord("expiry", 1, "mismatch")
	v.Settings.FailureThreshold = 1
	v.Observation.StartedAt = time.Now().Add(-10 * time.Minute)
	opened, err := repo.Record(ctx, v)
	require.NoError(t, err)
	require.NotNil(t, opened)
	_, err = db.Exec(`UPDATE image_resolution_quality_state SET cooldown_until=NOW()-INTERVAL '1 minute' WHERE requested_model='expiry'`)
	require.NoError(t, err)
	stored, err := repo.GetCooldown(ctx, 12, "expiry", "4K")
	require.NoError(t, err)
	require.Nil(t, stored)
	// A result from the original invocation window cannot create a new cycle.
	late := v
	late.Observation.ID = "old-after-expiry"
	late.Observation.StartedAt = time.Now().Add(-2 * time.Minute)
	late.Now = time.Now()
	again, err := repo.Record(ctx, late)
	require.NoError(t, err)
	require.Nil(t, again)
	fresh := imageResolutionPGRecord("expiry", 2, "mismatch")
	fresh.Settings.FailureThreshold = 1
	again, err = repo.Record(ctx, fresh)
	require.NoError(t, err)
	require.NotNil(t, again)
	require.NotEqual(t, opened.CycleID, again.CycleID)
	// A stale release cannot clear a newer cycle.
	require.NoError(t, repo.ClearCooldown(ctx, 12, opened.CycleID, time.Now()))
	stored, err = repo.GetCooldown(ctx, 12, "expiry", "4K")
	require.NoError(t, err)
	require.Equal(t, again.CycleID, stored.CycleID)
}

func TestImageResolutionGuardPostgresLateSuccessAndFinalLeaseFailure(t *testing.T) {
	db := imageResolutionPostgresDB(t)
	repo := NewImageResolutionGuardRepository(db)
	ctx := context.Background()
	start := time.Now().Add(-time.Minute)
	first := imageResolutionPGRecord("ordering", 1, "mismatch")
	first.Settings.FailureThreshold = 3
	first.Observation.StartedAt = start.Add(2 * time.Second)
	_, err := repo.Record(ctx, first)
	require.NoError(t, err)
	oldPass := imageResolutionPGRecord("ordering", 2, "pass")
	oldPass.Observation.StartedAt = start.Add(time.Second)
	_, err = repo.Record(ctx, oldPass)
	require.NoError(t, err)
	var n int
	require.NoError(t, db.QueryRow(`SELECT failure_count FROM image_resolution_quality_state WHERE requested_model='ordering'`).Scan(&n))
	require.Equal(t, 1, n)
	newPass := imageResolutionPGRecord("ordering", 3, "pass")
	newPass.Observation.StartedAt = start.Add(3 * time.Second)
	_, err = repo.Record(ctx, newPass)
	require.NoError(t, err)
	oldBad := imageResolutionPGRecord("ordering", 4, "mismatch")
	oldBad.Observation.StartedAt = start.Add(2500 * time.Millisecond)
	_, err = repo.Record(ctx, oldBad)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT failure_count FROM image_resolution_quality_state WHERE requested_model='ordering'`).Scan(&n))
	require.Zero(t, n)
	mail := imageResolutionPGRecord("abandoned-mail", 1, "mismatch")
	mail.Settings.FailureThreshold = 1
	_, err = repo.Record(ctx, mail)
	require.NoError(t, err)
	for i := 1; i <= 3; i++ {
		events, err := repo.ClaimMail(ctx, fmt.Sprintf("worker%d", i), 1, 2*time.Minute)
		require.NoError(t, err)
		require.Len(t, events, 1)
		require.Equal(t, i, events[0].Attempts)
		_, err = db.Exec(`UPDATE image_resolution_quality_events SET claimed_at=NOW()-INTERVAL '3 minutes' WHERE id=$1`, events[0].ID)
		require.NoError(t, err)
	}
	events, err := repo.ClaimMail(ctx, "after-final-abandon", 1, 2*time.Minute)
	require.NoError(t, err)
	require.Empty(t, events)
	maintenance := repo.(service.ImageResolutionGuardMaintenanceRepository)
	failed, err := maintenance.FailedMailCount(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, failed)
}

// Opt-in two-phase probe lets the caller stop/restart its isolated PostgreSQL
// process between invocations, rather than simulating a restart with a fake.
func TestImageResolutionGuardPostgresExternalRestart(t *testing.T) {
	phase := os.Getenv("SUB2API_RESOLUTION_RESTART_PHASE")
	if phase == "" {
		t.Skip("set prepare/verify phase around an isolated PostgreSQL restart")
	}
	schema := os.Getenv("SUB2API_RESOLUTION_RESTART_SCHEMA")
	require.True(t, strings.HasPrefix(schema, "resolution_restart_"))
	require.NotContains(t, schema, "\x00")
	control := openAsyncImagePostgresTestDB(t)
	ctx := context.Background()
	if phase == "prepare" {
		_, err := control.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema))
		require.NoError(t, err)
	} else {
		require.Equal(t, "verify", phase)
		t.Cleanup(func() {
			_, err := control.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`)
			require.NoError(t, err)
		})
	}
	u, err := url.Parse(os.Getenv("SUB2API_TEST_POSTGRES_DSN"))
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo := NewImageResolutionGuardRepository(db)
	if phase == "prepare" {
		_, err = db.Exec(`CREATE TABLE accounts(id BIGINT PRIMARY KEY); INSERT INTO accounts VALUES(12)`)
		require.NoError(t, err)
		data, err := migrations.FS.ReadFile("231_ZJ_image_resolution_guard.sql")
		require.NoError(t, err)
		_, err = db.Exec(string(data))
		require.NoError(t, err)
		v := imageResolutionPGRecord("external-restart", 1, "mismatch")
		v.Settings.FailureThreshold = 1
		opened, err := repo.Record(ctx, v)
		require.NoError(t, err)
		require.NotNil(t, opened)
		events, err := repo.ClaimMail(ctx, "before-process-restart", 1, 2*time.Minute)
		require.NoError(t, err)
		require.Len(t, events, 1)
		require.NoError(t, repo.SaveMailProgress(ctx, events[0].ID, events[0].ClaimToken, []string{"one@example.com"}))
		_, err = db.Exec(`UPDATE image_resolution_quality_events SET claimed_at=NOW()-INTERVAL '3 minutes'`)
		require.NoError(t, err)
		return
	}
	cooldown, err := repo.GetCooldown(ctx, 12, "external-restart", "4K")
	require.NoError(t, err)
	require.NotNil(t, cooldown)
	events, err := repo.ClaimMail(ctx, "after-process-restart", 1, 2*time.Minute)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, 2, events[0].Attempts)
	require.Equal(t, []string{"one@example.com"}, events[0].Delivered)
	require.Equal(t, cooldown.CycleID, events[0].CycleID)
	require.Error(t, repo.SaveMailProgress(ctx, events[0].ID, "before-process-restart", nil))
	require.NoError(t, repo.FinishMail(ctx, events[0].ID, events[0].ClaimToken, "sent", []string{"one@example.com", "two@example.com"}, time.Now(), ""))
}
