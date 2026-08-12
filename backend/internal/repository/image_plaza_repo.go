package repository

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type imagePlazaRepository struct {
	db *sql.DB
}

func NewImagePlazaRepository(db *sql.DB) service.ImagePlazaRepository {
	return &imagePlazaRepository{db: db}
}

func (r *imagePlazaRepository) Create(ctx context.Context, item *service.ImagePlazaItem) error {
	const q = `
INSERT INTO image_plaza_items (
  user_id, prompt, title, model, size, quality, format, background, style,
  storage_path, content_type, file_size, visibility, created_at
) VALUES (
  $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14
) RETURNING id, created_at`

	storagePath := item.StoragePath
	if storagePath == "" {
		storagePath = "pending"
	}
	now := item.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return r.db.QueryRowContext(
		ctx, q,
		item.UserID,
		item.Prompt,
		item.Title,
		item.Model,
		item.Size,
		item.Quality,
		item.Format,
		item.Background,
		item.Style,
		storagePath,
		item.ContentType,
		item.FileSize,
		item.Visibility,
		now,
	).Scan(&item.ID, &item.CreatedAt)
}

func (r *imagePlazaRepository) UpdateStorage(ctx context.Context, id int64, storagePath, contentType string, fileSize int64) error {
	const q = `UPDATE image_plaza_items SET storage_path=$2, content_type=$3, file_size=$4 WHERE id=$1`
	res, err := r.db.ExecContext(ctx, q, id, storagePath, contentType, fileSize)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return service.ErrImagePlazaNotFound
	}
	return nil
}

func (r *imagePlazaRepository) GetByID(ctx context.Context, id int64) (*service.ImagePlazaItem, error) {
	const q = `
SELECT i.id, i.user_id, COALESCE(u.email, ''), i.prompt, i.title, i.model, i.size, i.quality, i.format,
       i.background, i.style, i.storage_path, i.content_type, i.file_size, i.visibility, i.created_at
FROM image_plaza_items i
LEFT JOIN users u ON u.id = i.user_id
WHERE i.id = $1`

	item := &service.ImagePlazaItem{}
	err := r.db.QueryRowContext(ctx, q, id).Scan(
		&item.ID,
		&item.UserID,
		&item.UserEmail,
		&item.Prompt,
		&item.Title,
		&item.Model,
		&item.Size,
		&item.Quality,
		&item.Format,
		&item.Background,
		&item.Style,
		&item.StoragePath,
		&item.ContentType,
		&item.FileSize,
		&item.Visibility,
		&item.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, service.ErrImagePlazaNotFound
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (r *imagePlazaRepository) ListPublic(ctx context.Context, params service.ImagePlazaListParams) (*service.ImagePlazaListResult, error) {
	page := params.Page
	if page <= 0 {
		page = 1
	}
	pageSize := params.PageSize
	if pageSize <= 0 {
		pageSize = 24
	}
	offset := (page - 1) * pageSize
	query := strings.TrimSpace(params.Query)

	var (
		total int64
		rows  *sql.Rows
		err   error
	)

	if query == "" {
		err = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_plaza_items WHERE visibility='public'`).Scan(&total)
		if err != nil {
			return nil, err
		}
		rows, err = r.db.QueryContext(ctx, `
SELECT i.id, i.user_id, COALESCE(u.email, ''), i.prompt, i.title, i.model, i.size, i.quality, i.format,
       i.background, i.style, i.storage_path, i.content_type, i.file_size, i.visibility, i.created_at
FROM image_plaza_items i
LEFT JOIN users u ON u.id = i.user_id
WHERE i.visibility='public'
ORDER BY i.created_at DESC
LIMIT $1 OFFSET $2`, pageSize, offset)
	} else {
		like := "%" + query + "%"
		err = r.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM image_plaza_items
WHERE visibility='public'
  AND (prompt ILIKE $1 OR title ILIKE $1 OR model ILIKE $1)`, like).Scan(&total)
		if err != nil {
			return nil, err
		}
		rows, err = r.db.QueryContext(ctx, `
SELECT i.id, i.user_id, COALESCE(u.email, ''), i.prompt, i.title, i.model, i.size, i.quality, i.format,
       i.background, i.style, i.storage_path, i.content_type, i.file_size, i.visibility, i.created_at
FROM image_plaza_items i
LEFT JOIN users u ON u.id = i.user_id
WHERE i.visibility='public'
  AND (i.prompt ILIKE $1 OR i.title ILIKE $1 OR i.model ILIKE $1)
ORDER BY i.created_at DESC
LIMIT $2 OFFSET $3`, like, pageSize, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]service.ImagePlazaItem, 0, pageSize)
	for rows.Next() {
		var item service.ImagePlazaItem
		if scanErr := rows.Scan(
			&item.ID,
			&item.UserID,
			&item.UserEmail,
			&item.Prompt,
			&item.Title,
			&item.Model,
			&item.Size,
			&item.Quality,
			&item.Format,
			&item.Background,
			&item.Style,
			&item.StoragePath,
			&item.ContentType,
			&item.FileSize,
			&item.Visibility,
			&item.CreatedAt,
		); scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &service.ImagePlazaListResult{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (r *imagePlazaRepository) Delete(ctx context.Context, id int64, userID int64) (bool, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM image_plaza_items WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
