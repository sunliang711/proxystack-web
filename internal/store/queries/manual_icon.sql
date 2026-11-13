-- name: GetManualIcon :one
SELECT * FROM manual_icons WHERE kind = ? AND name = ?;

-- name: PutManualIcon :exec
INSERT INTO manual_icons (kind, name, content_type, data, updated_at, updated_by) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (kind, name) DO UPDATE SET content_type = excluded.content_type, data = excluded.data,
    updated_at = excluded.updated_at, updated_by = excluded.updated_by;

-- name: DeleteManualIcon :execrows
DELETE FROM manual_icons WHERE kind = ? AND name = ?;

-- name: ListManualIcons :many
SELECT kind, name, content_type, LENGTH(data) AS size, updated_at, updated_by FROM manual_icons ORDER BY kind, name;
