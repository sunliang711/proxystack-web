-- name: GetFavicon :one
SELECT * FROM favicons WHERE domain = ?;

-- name: PutFavicon :exec
INSERT INTO favicons (domain, ok, content_type, data, fetched_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (domain) DO UPDATE SET ok = excluded.ok, content_type = excluded.content_type,
    data = excluded.data, fetched_at = excluded.fetched_at, expires_at = excluded.expires_at;

-- name: DeleteFaviconsBefore :exec
DELETE FROM favicons WHERE fetched_at < ?;
