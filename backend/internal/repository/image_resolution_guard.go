package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type imageResolutionGuardRepository struct{ db *sql.DB }

func NewImageResolutionGuardRepository(db *sql.DB) service.ImageResolutionGuardRepository {
	return &imageResolutionGuardRepository{db: db}
}

func (r *imageResolutionGuardRepository) Record(ctx context.Context, record service.ImageResolutionRecord) (*service.ImageResolutionCooldown, error) {
	v := record.Observation
	now := record.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	observation, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	recipients, err := json.Marshal(append([]string{}, record.Recipients...))
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var eventID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO image_resolution_quality_events
 (observation_id,account_id,requested_model,size_tier,outcome,observation,min_area_percent,recipients,created_at,available_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9) ON CONFLICT(observation_id) DO NOTHING RETURNING id`, v.ID, v.AccountID, v.Expectation.RequestedModel, v.Expectation.SizeTier, record.Outcome, observation, record.Settings.MinAreaPercent, recipients, now).Scan(&eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	if record.Outcome == "unknown" {
		return nil, tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO image_resolution_quality_state(account_id,requested_model,size_tier) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, v.AccountID, v.Expectation.RequestedModel, v.Expectation.SizeTier)
	if err != nil {
		return nil, err
	}
	var failures int
	var cycle string
	var until sql.NullTime
	var ignore, last, lastPass time.Time
	err = tx.QueryRowContext(ctx, `SELECT failure_count,cycle_id,cooldown_until,ignore_before,last_started_at,last_pass_started_at FROM image_resolution_quality_state WHERE account_id=$1 AND requested_model=$2 AND size_tier=$3 FOR UPDATE`, v.AccountID, v.Expectation.RequestedModel, v.Expectation.SizeTier).Scan(&failures, &cycle, &until, &ignore, &last, &lastPass)
	if err != nil {
		return nil, err
	}
	// A completed old request cannot reverse a manual clear, reset a newer
	// streak, or open another cooling window after the original one expires.
	if v.StartedAt.Before(ignore) || (record.Outcome == "pass" && v.StartedAt.Before(last)) || (record.Outcome == "mismatch" && v.StartedAt.Before(lastPass)) {
		return nil, tx.Commit()
	}
	if until.Valid && now.Before(until.Time) {
		return nil, tx.Commit()
	}
	if until.Valid && v.StartedAt.Before(until.Time) {
		return nil, tx.Commit()
	}
	if until.Valid {
		failures = 0
	}
	if record.Outcome == "pass" {
		failures = 0
		lastPass = v.StartedAt
	} else {
		failures++
	}
	var cooldown *service.ImageResolutionCooldown
	var cooldownUntil any
	if record.Outcome == "mismatch" && failures >= record.Settings.FailureThreshold {
		cycle = uuid.NewString()
		deadline := now.Add(time.Duration(record.Settings.CooldownSeconds) * time.Second)
		cooldownUntil = deadline
		cooldown = &service.ImageResolutionCooldown{AccountID: v.AccountID, Model: v.Expectation.RequestedModel, SizeTier: v.Expectation.SizeTier, CycleID: cycle, CooldownUntil: deadline, FailureCount: failures, Reason: "image_resolution_downgrade"}
		imageResolutionCooldownEvidence(cooldown, v)
		status := "none"
		if record.Settings.EmailEnabled {
			status = "pending"
		}
		_, err = tx.ExecContext(ctx, `UPDATE image_resolution_quality_events SET cycle_id=$2,cooldown_until=$3,mail_status=$4 WHERE id=$1`, eventID, cycle, deadline, status)
		if err != nil {
			return nil, err
		}
	} else {
		cycle = ""
	}
	if v.StartedAt.After(last) {
		last = v.StartedAt
	}
	_, err = tx.ExecContext(ctx, `UPDATE image_resolution_quality_state SET failure_count=$4,cycle_id=$5,cooldown_until=$6,last_started_at=$7,updated_at=$8,last_pass_started_at=$9 WHERE account_id=$1 AND requested_model=$2 AND size_tier=$3`, v.AccountID, v.Expectation.RequestedModel, v.Expectation.SizeTier, failures, cycle, cooldownUntil, last, now, lastPass)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return cooldown, nil
}

func scanImageResolutionCooldown(row interface{ Scan(...any) error }) (*service.ImageResolutionCooldown, error) {
	v := &service.ImageResolutionCooldown{Reason: "image_resolution_downgrade"}
	var raw []byte
	err := row.Scan(&v.AccountID, &v.Model, &v.SizeTier, &v.CycleID, &v.CooldownUntil, &v.FailureCount, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var observation service.ImageResolutionObservation
	if json.Unmarshal(raw, &observation) == nil {
		imageResolutionCooldownEvidence(v, observation)
	}
	return v, nil
}

func imageResolutionCooldownEvidence(v *service.ImageResolutionCooldown, observation service.ImageResolutionObservation) {
	v.RequestedWidth = observation.Expectation.ExpectedWidth
	v.RequestedHeight = observation.Expectation.ExpectedHeight
	expected := observation.Expectation.ExpectedPixels
	if expected <= 0 {
		expected = int64(v.RequestedWidth) * int64(v.RequestedHeight)
	}
	for _, out := range observation.Outputs {
		if out.Width <= 0 || out.Height <= 0 {
			continue
		}
		area := int64(out.Width) * int64(out.Height)
		if v.ActualWidth == 0 || area < int64(v.ActualWidth)*int64(v.ActualHeight) {
			v.ActualWidth = out.Width
			v.ActualHeight = out.Height
		}
	}
	if expected > 0 {
		v.AreaPercent = 100 * float64(v.ActualWidth) * float64(v.ActualHeight) / float64(expected)
	}
}

func (r *imageResolutionGuardRepository) GetCooldown(ctx context.Context, id int64, model, tier string) (*service.ImageResolutionCooldown, error) {
	return scanImageResolutionCooldown(r.db.QueryRowContext(ctx, `SELECT s.account_id,s.requested_model,s.size_tier,s.cycle_id,s.cooldown_until,s.failure_count,e.observation FROM image_resolution_quality_state s LEFT JOIN image_resolution_quality_events e ON e.cycle_id=s.cycle_id AND e.cycle_id<>'' WHERE s.account_id=$1 AND s.requested_model=$2 AND s.size_tier=$3 AND s.cooldown_until>NOW()`, id, model, tier))
}

func (r *imageResolutionGuardRepository) ListCooldowns(ctx context.Context, id int64) ([]service.ImageResolutionCooldown, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT s.account_id,s.requested_model,s.size_tier,s.cycle_id,s.cooldown_until,s.failure_count,e.observation FROM image_resolution_quality_state s LEFT JOIN image_resolution_quality_events e ON e.cycle_id=s.cycle_id AND e.cycle_id<>'' WHERE s.account_id=$1 AND s.cooldown_until>NOW() ORDER BY s.requested_model,s.size_tier`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []service.ImageResolutionCooldown{}
	for rows.Next() {
		v, err := scanImageResolutionCooldown(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func (r *imageResolutionGuardRepository) ClearCooldown(ctx context.Context, id int64, cycle string, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE image_resolution_quality_state SET failure_count=0,cooldown_until=NULL,cycle_id='',ignore_before=$3,last_started_at=$3,updated_at=$3 WHERE account_id=$1 AND cycle_id=$2`, id, cycle, now)
	return err
}

func (r *imageResolutionGuardRepository) ClaimMail(ctx context.Context, token string, limit int, lease time.Duration) ([]service.ImageResolutionMailEvent, error) {
	if limit < 1 {
		limit = 1
	}
	seconds := int64(lease / time.Second)
	if seconds < 60 {
		seconds = 120
	}
	// An abandoned final attempt is terminal rather than silently remaining
	// pending forever. Earlier attempts are recovered after their lease expires.
	_, err := r.db.ExecContext(ctx, `UPDATE image_resolution_quality_events SET mail_status='failed',last_error='mail_lease_expired',claim_token=NULL,claimed_at=NULL WHERE mail_status='pending' AND attempts>=3 AND claimed_at<NOW()-($1*INTERVAL '1 second')`, seconds)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `WITH candidates AS (
 SELECT id FROM image_resolution_quality_events WHERE mail_status='pending' AND attempts<3 AND available_at<=NOW()
 AND (claimed_at IS NULL OR claimed_at<NOW()-($3*INTERVAL '1 second')) ORDER BY id LIMIT $2 FOR UPDATE SKIP LOCKED)
 UPDATE image_resolution_quality_events e SET claimed_at=NOW(),claim_token=$1,attempts=e.attempts+1 FROM candidates c WHERE e.id=c.id
 RETURNING e.id,e.cycle_id,e.observation,e.cooldown_until,e.min_area_percent,e.recipients,e.delivered,e.attempts`, token, limit, seconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []service.ImageResolutionMailEvent{}
	for rows.Next() {
		var v service.ImageResolutionMailEvent
		var observation, recipients, delivered []byte
		if err = rows.Scan(&v.ID, &v.CycleID, &observation, &v.CooldownUntil, &v.MinAreaPercent, &recipients, &delivered, &v.Attempts); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(observation, &v.Observation); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(recipients, &v.Recipients); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(delivered, &v.Delivered); err != nil {
			return nil, err
		}
		v.ClaimToken = token
		out = append(out, v)
	}
	return out, rows.Err()
}

func imageResolutionOwned(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("image resolution mail claim no longer owned")
	}
	return nil
}
func (r *imageResolutionGuardRepository) SaveMailProgress(ctx context.Context, id int64, token string, delivered []string) error {
	raw, err := json.Marshal(append([]string{}, delivered...))
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE image_resolution_quality_events SET delivered=$3,claimed_at=NOW() WHERE id=$1 AND claim_token=$2 AND mail_status='pending'`, id, token, raw)
	return imageResolutionOwned(result, err)
}
func (r *imageResolutionGuardRepository) FinishMail(ctx context.Context, id int64, token, status string, delivered []string, next time.Time, reason string) error {
	switch status {
	case "sent", "failed", "skipped", "pending":
	default:
		return fmt.Errorf("invalid mail status %q", status)
	}
	raw, err := json.Marshal(append([]string{}, delivered...))
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE image_resolution_quality_events SET mail_status=$3,delivered=$4,available_at=$5,last_error=$6,claim_token=NULL,claimed_at=NULL WHERE id=$1 AND claim_token=$2 AND mail_status='pending'`, id, token, status, raw, next, reason)
	return imageResolutionOwned(result, err)
}

func (r *imageResolutionGuardRepository) Prune(ctx context.Context, before time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM image_resolution_quality_events WHERE id IN (SELECT e.id FROM image_resolution_quality_events e WHERE e.created_at<$1 AND e.mail_status<>'pending' AND e.claim_token IS NULL AND NOT EXISTS (SELECT 1 FROM image_resolution_quality_state s WHERE s.cycle_id=e.cycle_id AND s.cooldown_until>NOW()) ORDER BY e.id LIMIT 1000)`, before)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
func (r *imageResolutionGuardRepository) FailedMailCount(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_resolution_quality_events WHERE mail_status='failed' AND created_at>NOW()-INTERVAL '7 days'`).Scan(&n)
	return n, err
}
