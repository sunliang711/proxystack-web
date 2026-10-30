-- name: ListManualNodes :many
SELECT * FROM manual_nodes ORDER BY position, id;

-- name: DeleteManualNodes :exec
DELETE FROM manual_nodes;

-- name: InsertManualNode :one
INSERT INTO manual_nodes (id, user_id, position, data) VALUES (nullif(?, 0), ?, ?, ?)
RETURNING id;

-- name: ListRemoteSources :many
SELECT * FROM remote_sources ORDER BY position, id;

-- name: DeleteRemoteSourcesExcept :exec
DELETE FROM remote_sources WHERE id NOT IN (sqlc.slice('ids'));

-- name: DeleteAllRemoteSources :exec
DELETE FROM remote_sources;

-- name: UpsertRemoteSource :one
INSERT INTO remote_sources (id, position, name, url, token, enabled) VALUES (nullif(?, 0), ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET position = excluded.position, name = excluded.name, url = excluded.url,
    token = excluded.token, enabled = excluded.enabled
RETURNING id;

-- name: ListSubTemplates :many
SELECT * FROM sub_templates;

-- name: PutSubTemplate :exec
INSERT INTO sub_templates (format, content) VALUES (?, ?)
ON CONFLICT (format) DO UPDATE SET content = excluded.content;

-- name: DeleteSubTemplate :exec
DELETE FROM sub_templates WHERE format = ?;

-- name: ListRemoteSourceStates :many
SELECT * FROM remote_source_state;

-- name: PutRemoteSourceState :exec
INSERT INTO remote_source_state (source_id, last_sync_at, last_error, node_count, nodes) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (source_id) DO UPDATE SET last_sync_at = excluded.last_sync_at, last_error = excluded.last_error,
    node_count = excluded.node_count, nodes = excluded.nodes;

-- name: SetRemoteSourceError :exec
INSERT INTO remote_source_state (source_id, last_sync_at, last_error, node_count, nodes) VALUES (?, ?, ?, 0, '[]')
ON CONFLICT (source_id) DO UPDATE SET last_sync_at = excluded.last_sync_at, last_error = excluded.last_error;
