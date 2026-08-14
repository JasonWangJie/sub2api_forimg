package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type apiKeyPlatformGroupRepository struct {
	db *sql.DB
}

// NewAPIKeyPlatformGroupRepository creates a raw-SQL repository for api_key_platform_groups.
func NewAPIKeyPlatformGroupRepository(db *sql.DB) service.APIKeyPlatformGroupRepository {
	return &apiKeyPlatformGroupRepository{db: db}
}

func (r *apiKeyPlatformGroupRepository) ListByAPIKeyID(ctx context.Context, apiKeyID int64) ([]service.APIKeyPlatformGroup, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("api key platform group repository is not configured")
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT api_key_id, platform, group_id
FROM api_key_platform_groups
WHERE api_key_id = $1
ORDER BY platform`, apiKeyID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanAPIKeyPlatformGroups(rows)
}

func (r *apiKeyPlatformGroupRepository) ListByAPIKeyIDs(ctx context.Context, apiKeyIDs []int64) (map[int64][]service.APIKeyPlatformGroup, error) {
	out := make(map[int64][]service.APIKeyPlatformGroup, len(apiKeyIDs))
	if r == nil || r.db == nil {
		return nil, errors.New("api key platform group repository is not configured")
	}
	if len(apiKeyIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT api_key_id, platform, group_id
FROM api_key_platform_groups
WHERE api_key_id = ANY($1)
ORDER BY api_key_id, platform`, pq.Array(apiKeyIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items, err := scanAPIKeyPlatformGroups(rows)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		out[item.APIKeyID] = append(out[item.APIKeyID], item)
	}
	return out, nil
}

func (r *apiKeyPlatformGroupRepository) ReplaceForAPIKey(ctx context.Context, apiKeyID int64, mappings map[string]int64) error {
	if r == nil || r.db == nil {
		return errors.New("api key platform group repository is not configured")
	}
	if apiKeyID <= 0 {
		return fmt.Errorf("invalid api key id")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM api_key_platform_groups WHERE api_key_id = $1`, apiKeyID); err != nil {
		return err
	}
	for platform, groupID := range mappings {
		platform = strings.ToLower(strings.TrimSpace(platform))
		if platform == "" || groupID <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO api_key_platform_groups (api_key_id, platform, group_id)
VALUES ($1, $2, $3)`, apiKeyID, platform, groupID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *apiKeyPlatformGroupRepository) ClearByGroupID(ctx context.Context, groupID int64) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("api key platform group repository is not configured")
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM api_key_platform_groups WHERE group_id = $1`, groupID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func scanAPIKeyPlatformGroups(rows *sql.Rows) ([]service.APIKeyPlatformGroup, error) {
	out := make([]service.APIKeyPlatformGroup, 0)
	for rows.Next() {
		var item service.APIKeyPlatformGroup
		if err := rows.Scan(&item.APIKeyID, &item.Platform, &item.GroupID); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

var _ service.APIKeyPlatformGroupRepository = (*apiKeyPlatformGroupRepository)(nil)
