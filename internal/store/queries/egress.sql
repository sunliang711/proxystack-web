-- name: GetStackEgressHealth :one
SELECT * FROM stack_egress_health WHERE stack_id = ?;

-- name: ListStackEgressHealth :many
SELECT * FROM stack_egress_health ORDER BY stack_id;

-- name: UpsertStackEgressHealth :exec
INSERT INTO stack_egress_health (stack_id, status, checked_at, failures, failure_since, incident, alarmed, fingerprint, ipv4, ipv6, ipv4_error, ipv6_error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (stack_id) DO UPDATE SET
    status = excluded.status, checked_at = excluded.checked_at, failures = excluded.failures,
    failure_since = excluded.failure_since, incident = excluded.incident, alarmed = excluded.alarmed,
    fingerprint = excluded.fingerprint, ipv4 = excluded.ipv4, ipv6 = excluded.ipv6,
    ipv4_error = excluded.ipv4_error, ipv6_error = excluded.ipv6_error;

-- name: SupersedeStackNotifications :exec
UPDATE notifications SET status = 'skipped', error = 'Superseded by a newer egress state'
WHERE stack_id = ? AND status IN ('pending', 'failed');
