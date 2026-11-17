-- name: ListMihomoProviders :many
SELECT * FROM mihomo_providers ORDER BY stack_id, position, id;

-- name: DeleteMihomoProvidersExcept :exec
DELETE FROM mihomo_providers WHERE id NOT IN (sqlc.slice('ids'));

-- name: DeleteAllMihomoProviders :exec
DELETE FROM mihomo_providers;

-- name: UpsertMihomoProvider :one
INSERT INTO mihomo_providers (id, stack_id, position, config)
VALUES (nullif(?, 0), ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET stack_id = excluded.stack_id, position = excluded.position, config = excluded.config
RETURNING id;

-- name: ListMihomoProviderState :many
SELECT * FROM mihomo_provider_state;

-- name: PutMihomoProviderState :exec
INSERT INTO mihomo_provider_state (provider_id, config_hash, last_attempt_at, last_success_at, last_error)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(provider_id) DO UPDATE SET config_hash = excluded.config_hash,
    last_attempt_at = excluded.last_attempt_at, last_success_at = excluded.last_success_at, last_error = excluded.last_error;
