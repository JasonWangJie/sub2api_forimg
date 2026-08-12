package repository

import (
	"context"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

var asyncImageUploadReservationTestColumns = []string{
	"id", "reservation_id", "user_id", "api_key_id", "idempotency_key", "request_hash",
	"byte_size", "status", "input_object_id", "failure_reason", "lease_expires_at",
	"reserved_at", "completed_at", "failed_at", "created_at", "updated_at",
	"intent_provider", "intent_bucket", "intent_object_key", "intent_content_type",
	"intent_byte_size", "intent_checksum", "cleanup_claimed_at", "cleanup_delete_count",
	"last_deleted_at", "idempotency_expires_at",
}

func TestAsyncImageUploadReservationMigrationDefinesDurableAdmissionState(t *testing.T) {
	content, err := migrations.FS.ReadFile("187_ZJ_async_image_upload_reservations.sql")
	require.NoError(t, err)
	sqlText := string(content)
	for _, expected := range []string{
		"CREATE TABLE IF NOT EXISTS async_image_upload_reservations",
		"async_image_upload_reservations_owner_idempotency_uidx",
		"async_image_upload_reservations_failed_cleanup_idx",
		"CREATE TABLE IF NOT EXISTS async_image_upload_attempts",
		"async_image_upload_attempts_owner_time_idx",
		"async_image_upload_attempts_cleanup_idx",
		"CREATE TABLE IF NOT EXISTS async_image_input_url_aliases",
		"REFERENCES async_image_input_objects(id) ON DELETE SET NULL",
		"intent_object_key TEXT",
		"cleanup_claimed_at TIMESTAMPTZ",
		"cleanup_delete_count SMALLINT NOT NULL DEFAULT 0",
		"last_deleted_at TIMESTAMPTZ",
		"async_image_upload_reservations_delete_state_check",
		"cleanup_delete_count = 0 AND last_deleted_at IS NULL",
		"cleanup_delete_count = 1 AND last_deleted_at IS NOT NULL",
		"admission_id VARCHAR(64) NOT NULL UNIQUE",
		"Signed-URL ownership tombstones",
	} {
		require.Contains(t, sqlText, expected)
	}
}

func TestReserveAsyncImageUploadSerializesActiveBytesWithAdmission(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	lease := now.Add(15 * time.Minute)
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(asyncImageUploadAdvisoryLockBase + int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE async_image_upload_reservations.*reservation_expired").WithArgs(int64(20), now).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("(?s)WITH stale AS.*intent_object_key IS NULL.*status='failed'.*DELETE FROM async_image_upload_reservations").WithArgs(now).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("UPDATE async_image_upload_attempts SET consumed_at").
		WithArgs("asyncimg_admit", int64(10), int64(20), now).
		WillReturnRows(sqlmock.NewRows([]string{"admission_id"}).AddRow("asyncimg_admit"))
	mock.ExpectQuery(`SELECT.*SUM\(byte_size\).*async_image_input_objects.*SUM\(CASE.*intent_byte_size.*async_image_upload_reservations.*status='failed'.*intent_object_key IS NOT NULL`).
		WithArgs(int64(20), now).WillReturnRows(sqlmock.NewRows([]string{"stored", "reserved"}).AddRow(int64(300), int64(100)))
	mock.ExpectQuery("INSERT INTO async_image_upload_reservations").
		WithArgs("asyncimg_upload", int64(10), int64(20), nil, hash, int64(200), lease, now).
		WillReturnRows(sqlmock.NewRows(asyncImageUploadReservationTestColumns).AddRow(
			int64(1), "asyncimg_upload", int64(10), int64(20), nil, hash,
			int64(200), service.AsyncImageUploadStatusReserved, nil, nil, lease,
			now, nil, nil, now, now, nil, nil, nil, nil, nil, nil, nil, 0, nil, nil,
		))
	mock.ExpectExec("UPDATE async_image_upload_attempts SET reservation_id").
		WithArgs("asyncimg_admit", "asyncimg_upload", now).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	result, err := repo.ReserveAsyncImageUpload(context.Background(), service.ReserveAsyncImageUploadParams{
		AdmissionID: "asyncimg_admit", ReservationID: "asyncimg_upload", UserID: 10, APIKeyID: 20,
		RequestHash: hash, ByteSize: 200, UploadPerMinute: 20,
		MaxInputBytesPerKey: 1000, Now: now, LeaseExpiresAt: lease,
	})
	require.NoError(t, err)
	require.False(t, result.Reused)
	require.Equal(t, "asyncimg_upload", result.Reservation.ReservationID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAdmitAsyncImageUploadRejectsAtRollingLimitWithoutGrowingAttempts(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("(?s)WITH stale AS.*attempted_at.*DELETE FROM async_image_upload_attempts").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM async_image_upload_attempts`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(20))
	mock.ExpectCommit()

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	_, err = repo.AdmitAsyncImageUpload(context.Background(), service.AdmitAsyncImageUploadParams{
		AdmissionID: "asyncimg_limited", UserID: 10, APIKeyID: 20,
		UploadPerMinute: 20, Now: now,
	})
	require.ErrorIs(t, err, service.ErrAsyncImageUploadRateLimited)
	require.NoError(t, mock.ExpectationsWereMet(), "a rejected attempt must not create unbounded rate rows")
}

func TestReserveAsyncImageUploadIncludesStoredAndInflightBytes(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE async_image_upload_reservations.*reservation_expired").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("(?s)WITH stale AS.*status='failed'.*DELETE FROM async_image_upload_reservations").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("UPDATE async_image_upload_attempts SET consumed_at").
		WillReturnRows(sqlmock.NewRows([]string{"admission_id"}).AddRow("asyncimg_admit"))
	mock.ExpectQuery(`SELECT.*SUM\(byte_size\).*async_image_input_objects.*SUM\(CASE.*intent_byte_size.*async_image_upload_reservations`).
		WillReturnRows(sqlmock.NewRows([]string{"stored", "reserved"}).AddRow(int64(850), int64(100)))
	mock.ExpectCommit()

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	_, err = repo.ReserveAsyncImageUpload(context.Background(), service.ReserveAsyncImageUploadParams{
		AdmissionID: "asyncimg_admit", ReservationID: "asyncimg_quota", UserID: 10, APIKeyID: 20,
		RequestHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ByteSize:    100, UploadPerMinute: 20, MaxInputBytesPerKey: 1000,
		Now: now, LeaseExpiresAt: now.Add(time.Minute),
	})
	require.ErrorIs(t, err, service.ErrAsyncImageUploadQuotaExceeded)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveAsyncImageUploadReplayConsumesAdmissionAndBypassesBytes(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	expiresAt := now.Add(time.Hour)
	hash := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	key := "same-upload"
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE async_image_upload_reservations.*reservation_expired").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("(?s)WITH stale AS.*status='failed'.*DELETE FROM async_image_upload_reservations").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("UPDATE async_image_upload_attempts SET consumed_at").
		WillReturnRows(sqlmock.NewRows([]string{"admission_id"}).AddRow("asyncimg_admit"))
	mock.ExpectQuery("SELECT.*FROM async_image_upload_reservations.*idempotency_key=.*FOR UPDATE").
		WithArgs(int64(20), key).
		WillReturnRows(sqlmock.NewRows(asyncImageUploadReservationTestColumns).AddRow(
			int64(1), "asyncimg_done", int64(10), int64(20), key, hash,
			int64(12), service.AsyncImageUploadStatusCompleted, int64(9), nil, nil,
			now.Add(-time.Minute), now, nil, now.Add(-time.Minute), now,
			nil, nil, nil, nil, nil, nil, nil, 0, nil, now.Add(24*time.Hour),
		))
	mock.ExpectQuery("SELECT id, upload_id.*FROM async_image_input_objects.*cleanup_claimed_at IS NULL").
		WithArgs(int64(9), int64(10), int64(20), now).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "upload_id", "user_id", "api_key_id", "provider", "bucket", "object_key",
			"content_type", "byte_size", "checksum", "width", "height", "url_hash", "filename",
			"expires_at", "cleanup_claimed_at", "created_at",
		}).AddRow(
			int64(9), "asyncimg_done", int64(10), int64(20), "aliyun", "images", "inputs/done.png",
			"image/png", int64(12), "checksum", 1, 1,
			"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "done.png",
			expiresAt, nil, now.Add(-time.Minute),
		))
	mock.ExpectExec("UPDATE async_image_upload_attempts SET reservation_id").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	result, err := repo.ReserveAsyncImageUpload(context.Background(), service.ReserveAsyncImageUploadParams{
		AdmissionID: "asyncimg_admit", ReservationID: "ignored", UserID: 10, APIKeyID: 20, IdempotencyKey: &key,
		RequestHash: hash, ByteSize: 12, UploadPerMinute: 20, MaxInputBytesPerKey: 1000,
		Now: now, LeaseExpiresAt: now.Add(time.Minute),
	})
	require.NoError(t, err)
	require.True(t, result.Reused)
	require.Equal(t, int64(9), result.InputObject.ID)
	require.NoError(t, mock.ExpectationsWereMet(), "completed replay must consume admission but bypass byte reservation")
}

func TestRegisterAsyncImageInputURLAliasRequiresOwnedLiveObject(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	expiresAt := time.Now().UTC().Add(time.Hour)
	hash := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM async_image_input_objects.*FOR UPDATE").
		WithArgs(int64(9), int64(10), int64(20)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(9)))
	mock.ExpectQuery("SELECT input_object_id FROM async_image_input_url_aliases").
		WithArgs(hash).WillReturnRows(sqlmock.NewRows([]string{"input_object_id"}))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM async_image_input_url_aliases WHERE input_object_id=\\$1$").
		WithArgs(int64(9)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("INSERT INTO async_image_input_url_aliases").
		WithArgs(hash, int64(9), expiresAt).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	err = repo.RegisterAsyncImageInputURLAlias(context.Background(), service.RegisterAsyncImageInputURLAliasParams{
		InputObjectID: 9, UserID: 10, APIKeyID: 20, URLHash: hash, ExpiresAt: expiresAt,
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRegisterAsyncImageInputURLAliasRejectsBeyondPerObjectLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	expiresAt := time.Now().UTC().Add(time.Hour)
	hash := "abababababababababababababababababababababababababababababababab"
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM async_image_input_objects.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(9)))
	mock.ExpectQuery("SELECT input_object_id FROM async_image_input_url_aliases").
		WillReturnRows(sqlmock.NewRows([]string{"input_object_id"}))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM async_image_input_url_aliases WHERE input_object_id=\\$1$").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(maxAsyncImageInputURLAliases))
	mock.ExpectRollback()

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	err = repo.RegisterAsyncImageInputURLAlias(context.Background(), service.RegisterAsyncImageInputURLAliasParams{
		InputObjectID: 9, UserID: 10, APIKeyID: 20, URLHash: hash, ExpiresAt: expiresAt,
	})
	require.ErrorIs(t, err, service.ErrAsyncImageUploadAliasLimit)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompleteAsyncImageUploadAtomicallyRegistersObjectAndClearsIntent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	expiresAt := now.Add(24 * time.Hour)
	hash := "abababababababababababababababababababababababababababababababab"
	urlHash := "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
	checksum := "efefefefefefefefefefefefefefefefefefefefefefefefefefefefefefefef"
	ref := service.ObjectRef{
		Provider: "aliyun", Bucket: "images", ObjectKey: "inputs/20/upload.png",
		ContentType: "image/png", SizeBytes: 12, ChecksumSHA256: checksum, Width: 1, Height: 1,
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT.*async_image_upload_reservations.*reservation_id=.*FOR UPDATE").
		WithArgs("asyncimg_upload").
		WillReturnRows(sqlmock.NewRows(asyncImageUploadReservationTestColumns).AddRow(
			int64(1), "asyncimg_upload", int64(10), int64(20), nil, hash,
			int64(12), service.AsyncImageUploadStatusReserved, nil, nil, now.Add(time.Hour),
			now, nil, nil, now, now,
			ref.Provider, ref.Bucket, ref.ObjectKey, ref.ContentType, ref.SizeBytes, ref.ChecksumSHA256,
			nil, 0, nil, nil,
		))
	mock.ExpectQuery("INSERT INTO async_image_input_objects").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "upload_id", "user_id", "api_key_id", "provider", "bucket", "object_key",
			"content_type", "byte_size", "checksum", "width", "height", "url_hash", "filename",
			"expires_at", "cleanup_claimed_at", "created_at",
		}).AddRow(
			int64(9), "asyncimg_upload", int64(10), int64(20), ref.Provider, ref.Bucket, ref.ObjectKey,
			ref.ContentType, ref.SizeBytes, ref.ChecksumSHA256, 1, 1, urlHash, "reference.png",
			expiresAt, nil, now,
		))
	mock.ExpectExec(`(?s)UPDATE async_image_upload_reservations SET.*status='completed'.*intent_provider=NULL.*idempotency_expires_at=\$3`).
		WithArgs(int64(1), int64(9), expiresAt).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	object, err := repo.CompleteAsyncImageUpload(context.Background(), service.CompleteAsyncImageUploadParams{
		ReservationID: "asyncimg_upload", UserID: 10, APIKeyID: 20, RequestHash: hash,
		ObjectRef: ref, URLHash: urlHash, Filename: "reference.png", ExpiresAt: expiresAt,
	})
	require.NoError(t, err)
	require.Equal(t, int64(9), object.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveAsyncImageUploadReturnsResultUnavailableForExpiredCompletedObject(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	hash := "1212121212121212121212121212121212121212121212121212121212121212"
	key := "completed-expired"
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE async_image_upload_reservations.*reservation_expired").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("(?s)WITH stale AS.*DELETE FROM async_image_upload_reservations").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("UPDATE async_image_upload_attempts SET consumed_at").
		WillReturnRows(sqlmock.NewRows([]string{"admission_id"}).AddRow("asyncimg_admit"))
	mock.ExpectQuery("SELECT.*async_image_upload_reservations.*idempotency_key=.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows(asyncImageUploadReservationTestColumns).AddRow(
			int64(1), "asyncimg_old", int64(10), int64(20), key, hash, int64(12),
			service.AsyncImageUploadStatusCompleted, int64(9), nil, nil, now.Add(-time.Hour),
			now.Add(-time.Hour), nil, now.Add(-time.Hour), now,
			nil, nil, nil, nil, nil, nil, nil, 0, nil, now.Add(time.Hour),
		))
	mock.ExpectQuery("SELECT id, upload_id.*async_image_input_objects").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	_, err = repo.ReserveAsyncImageUpload(context.Background(), service.ReserveAsyncImageUploadParams{
		AdmissionID: "asyncimg_admit", ReservationID: "must-not-reuse", UserID: 10, APIKeyID: 20,
		IdempotencyKey: &key, RequestHash: hash, ByteSize: 12, UploadPerMinute: 20,
		MaxInputBytesPerKey: 1000, Now: now, LeaseExpiresAt: now.Add(time.Minute),
	})
	require.ErrorIs(t, err, service.ErrAsyncImageUploadResultUnavailable)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveAsyncImageUploadNeverClearsFailedOrphanIntent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	hash := "9090909090909090909090909090909090909090909090909090909090909090"
	key := "orphan-cleanup-pending"
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE async_image_upload_reservations.*intent_object_key IS NULL").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("(?s)WITH stale AS.*intent_object_key IS NULL.*DELETE FROM async_image_upload_reservations").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("UPDATE async_image_upload_attempts SET consumed_at").
		WillReturnRows(sqlmock.NewRows([]string{"admission_id"}).AddRow("asyncimg_admit"))
	mock.ExpectQuery("SELECT.*async_image_upload_reservations.*idempotency_key=.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows(asyncImageUploadReservationTestColumns).AddRow(
			int64(1), "asyncimg_orphan", int64(10), int64(20), key, hash, int64(12),
			service.AsyncImageUploadStatusFailed, nil, "put_failed", nil,
			now.Add(-time.Minute), nil, now.Add(-time.Minute), now.Add(-time.Minute), now,
			"aliyun", "images", "inputs/orphan.png", "image/png", int64(12),
			"9292929292929292929292929292929292929292929292929292929292929292",
			nil, 0, nil, nil,
		))
	mock.ExpectRollback()
	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	_, err = repo.ReserveAsyncImageUpload(context.Background(), service.ReserveAsyncImageUploadParams{
		AdmissionID: "asyncimg_admit", ReservationID: "must-not-reuse", UserID: 10, APIKeyID: 20,
		IdempotencyKey: &key, RequestHash: hash, ByteSize: 12, UploadPerMinute: 20,
		MaxInputBytesPerKey: 1000, Now: now, LeaseExpiresAt: now.Add(time.Minute),
	})
	require.ErrorIs(t, err, service.ErrAsyncImageUploadInProgress)
	require.NoError(t, mock.ExpectationsWereMet(), "pending intent must not be cleared or rebound")
}

func TestClaimAsyncImageUploadCleanupIntentsUsesPutDeleteGrace(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	staleBefore := now.Add(-30 * time.Minute)
	mock.ExpectQuery("(?s)cleanup_delete_count=0.*last_deleted_at <= \\$1::timestamptz - INTERVAL '10 minutes'.*status='failed'.*updated_at <= \\$1::timestamptz - INTERVAL '10 minutes'.*status='reserved'.*lease_expires_at <= \\$2::timestamptz.*FOR UPDATE SKIP LOCKED").
		WithArgs(now, staleBefore, 100).
		WillReturnRows(sqlmock.NewRows([]string{
			"reservation_id", "intent_provider", "intent_bucket", "intent_object_key",
			"intent_content_type", "intent_byte_size", "intent_checksum", "cleanup_claimed_at",
		}))

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	intents, err := repo.ClaimAsyncImageUploadCleanupIntents(context.Background(), now, staleBefore, 100)
	require.NoError(t, err)
	require.Empty(t, intents)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompleteAsyncImageUploadIntentDeletionRequiresTwoPasses(t *testing.T) {
	now := time.Now().UTC()

	t.Run("first delete keeps recovery fact", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT cleanup_delete_count.*FOR UPDATE").
			WithArgs("asyncimg_orphan", now).
			WillReturnRows(sqlmock.NewRows([]string{"cleanup_delete_count"}).AddRow(0))
		mock.ExpectExec("UPDATE async_image_upload_reservations SET.*cleanup_delete_count=1.*cleanup_claimed_at=NULL").
			WithArgs("asyncimg_orphan", now).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
		removed, err := repo.CompleteAsyncImageUploadIntentDeletion(context.Background(), "asyncimg_orphan", now)
		require.NoError(t, err)
		require.False(t, removed)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("second delete removes recovery fact", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT cleanup_delete_count.*FOR UPDATE").
			WithArgs("asyncimg_orphan", now).
			WillReturnRows(sqlmock.NewRows([]string{"cleanup_delete_count"}).AddRow(1))
		mock.ExpectExec("DELETE FROM async_image_upload_reservations.*cleanup_delete_count=1.*last_deleted_at <= NOW\\(\\) - INTERVAL '10 minutes'").
			WithArgs("asyncimg_orphan", now).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
		removed, err := repo.CompleteAsyncImageUploadIntentDeletion(context.Background(), "asyncimg_orphan", now)
		require.NoError(t, err)
		require.True(t, removed)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestAsyncImageUploadIntentIsPersistedBeforePutAndFailureReleasesBytes(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	hash := "3434343434343434343434343434343434343434343434343434343434343434"
	checksum := "5656565656565656565656565656565656565656565656565656565656565656"
	ref := service.ObjectRef{
		Provider: "tencent", Bucket: "images", ObjectKey: "inputs/20/u.png",
		ContentType: "image/png", SizeBytes: 12, ChecksumSHA256: checksum,
	}
	mock.ExpectExec("(?s)UPDATE async_image_upload_reservations SET.*intent_provider=\\$5.*status='reserved'").
		WithArgs("asyncimg_upload", int64(10), int64(20), hash, ref.Provider, ref.Bucket,
			ref.ObjectKey, ref.ContentType, ref.SizeBytes, ref.ChecksumSHA256).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("(?s)UPDATE async_image_upload_reservations SET.*status='failed'.*WHERE reservation_id=.*status='reserved'").
		WithArgs("asyncimg_upload", hash, "put_failed").WillReturnResult(sqlmock.NewResult(0, 1))

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	require.NoError(t, repo.SetAsyncImageUploadObjectIntent(context.Background(), service.SetAsyncImageUploadObjectIntentParams{
		ReservationID: "asyncimg_upload", UserID: 10, APIKeyID: 20, RequestHash: hash, ObjectRef: ref,
	}))
	released, err := repo.FailAsyncImageUpload(context.Background(), "asyncimg_upload", hash, "put_failed")
	require.NoError(t, err)
	require.True(t, released)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAsyncImageInputAliasAlwaysResolvesToOwnershipCheckEvenAfterSignatureExpiry(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`(?s)async_image_input_url_aliases.*a\.url_hash = ANY\(\$1\)\s*\)`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "upload_id", "user_id", "api_key_id", "provider", "bucket", "object_key",
			"content_type", "byte_size", "checksum", "width", "height", "url_hash", "filename",
			"expires_at", "cleanup_claimed_at", "created_at",
		}))
	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	objects, err := repo.FindAsyncImageInputObjectsByURLHashes(context.Background(), []string{
		"7878787878787878787878787878787878787878787878787878787878787878",
	})
	require.NoError(t, err)
	require.Empty(t, objects)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImageStorageIdentityGuardIncludesInputsAndUploadIntents(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("(?s)image_storage_objects.*async_image_input_objects.*async_image_result_upload_intents.*async_image_upload_reservations.*intent_object_key").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	repo := NewImageLibraryRepository(db)
	inUse, err := repo.HasActiveImageStorageObjects(context.Background())
	require.NoError(t, err)
	require.True(t, inUse)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteExpiredAsyncImageUploadStateIsGlobalAndBounded(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	mock.ExpectQuery("(?s)stale_attempts.*attempted_at < \\$1::timestamptz - INTERVAL '5 minutes'.*LIMIT \\$2.*stale_reservations.*intent_object_key IS NULL.*LIMIT \\$2").
		WithArgs(now, 100).
		WillReturnRows(sqlmock.NewRows([]string{"deleted"}).AddRow(int64(2)))
	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	deleted, err := repo.DeleteExpiredAsyncImageUploadAdmissionState(context.Background(), now, 100)
	require.NoError(t, err)
	require.Equal(t, int64(2), deleted)
	require.NoError(t, mock.ExpectationsWereMet())
}
