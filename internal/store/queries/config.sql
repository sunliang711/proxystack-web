-- Config tables are saved as a whole: child rows are deleted, users and stacks are upserted,
-- then child rows are re-inserted with their previous IDs so IDs stay stable.

-- name: ListUsers :many
SELECT * FROM users ORDER BY position, id;

-- name: ListUserProfiles :many
SELECT * FROM user_profiles ORDER BY user_id, position, id;

-- name: ListStacks :many
SELECT * FROM stacks ORDER BY position, id;

-- name: ListInbounds :many
SELECT * FROM inbounds ORDER BY stack_id, position, id;

-- name: ListInboundUserRefs :many
SELECT * FROM inbound_user_refs ORDER BY inbound_id, position;

-- name: ListMihomoListeners :many
SELECT * FROM mihomo_listeners ORDER BY stack_id, kind;

-- name: ListMihomoUpstreams :many
SELECT * FROM mihomo_upstreams ORDER BY stack_id, position, id;

-- name: ListMihomoGroups :many
SELECT * FROM mihomo_groups ORDER BY stack_id, position, id;

-- name: DeleteMihomoUpstreams :exec
DELETE FROM mihomo_upstreams;

-- name: DeleteMihomoGroups :exec
DELETE FROM mihomo_groups;

-- name: DeleteMihomoListeners :exec
DELETE FROM mihomo_listeners;

-- name: DeleteInboundUserRefs :exec
DELETE FROM inbound_user_refs;

-- name: DeleteInbounds :exec
DELETE FROM inbounds;

-- name: DeleteUserProfiles :exec
DELETE FROM user_profiles;

-- name: DeleteUsersExcept :exec
DELETE FROM users WHERE id NOT IN (sqlc.slice('ids'));

-- name: DeleteAllUsers :exec
DELETE FROM users;

-- name: DeleteStacksExcept :exec
DELETE FROM stacks WHERE id NOT IN (sqlc.slice('ids'));

-- name: DeleteAllStacks :exec
DELETE FROM stacks;

-- name: UpsertUser :one
INSERT INTO users (id, position, name, enabled, uuid, ss_password, remark, display_template, tag,
                   sub_token, quota_bytes, daily_limit_bytes, daily_limit_action, reset_mode, reset_day, expire_at, note)
VALUES (nullif(?, 0), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    position = excluded.position, name = excluded.name, enabled = excluded.enabled, uuid = excluded.uuid,
    ss_password = excluded.ss_password, remark = excluded.remark, display_template = excluded.display_template,
    tag = excluded.tag, sub_token = excluded.sub_token, quota_bytes = excluded.quota_bytes,
    daily_limit_bytes = excluded.daily_limit_bytes, daily_limit_action = excluded.daily_limit_action,
    reset_mode = excluded.reset_mode, reset_day = excluded.reset_day, expire_at = excluded.expire_at,
    note = excluded.note
RETURNING id;

-- name: InsertUserProfile :one
INSERT INTO user_profiles (id, user_id, position, name, uuid, ss_password, remark, display_template, tag)
VALUES (nullif(?, 0), ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: UpsertStack :one
INSERT INTO stacks (id, position, name, enabled, desired_running, role, labels,
                    xray_enabled, xray_loglevel, xray_api, xray_stats, xray_policy, xray_api_port,
                    outbound_type, outbound_server, outbound_port, outbound_username, outbound_password,
                    outbound_private_direct, mihomo_enabled, mihomo_mode, mihomo_loglevel,
                    controller_port, controller_secret, rule_profile, rule_final, rule_extra)
VALUES (nullif(?, 0), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    position = excluded.position, name = excluded.name, enabled = excluded.enabled,
    desired_running = excluded.desired_running, role = excluded.role, labels = excluded.labels,
    xray_enabled = excluded.xray_enabled, xray_loglevel = excluded.xray_loglevel, xray_api = excluded.xray_api,
    xray_stats = excluded.xray_stats, xray_policy = excluded.xray_policy, xray_api_port = excluded.xray_api_port,
    outbound_type = excluded.outbound_type, outbound_server = excluded.outbound_server,
    outbound_port = excluded.outbound_port, outbound_username = excluded.outbound_username,
    outbound_password = excluded.outbound_password, outbound_private_direct = excluded.outbound_private_direct,
    mihomo_enabled = excluded.mihomo_enabled, mihomo_mode = excluded.mihomo_mode,
    mihomo_loglevel = excluded.mihomo_loglevel, controller_port = excluded.controller_port,
    controller_secret = excluded.controller_secret, rule_profile = excluded.rule_profile,
    rule_final = excluded.rule_final, rule_extra = excluded.rule_extra
RETURNING id;

-- name: InsertInbound :one
INSERT INTO inbounds (id, stack_id, position, name, protocol, listen, port, sub, udp, network, ws_path,
                      ws_headers, grpc_service_name, method, password, auth_type, auth_username, auth_password,
                      server, remark, display_template, region, tag)
VALUES (nullif(?, 0), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: InsertInboundUserRef :exec
INSERT INTO inbound_user_refs (inbound_id, position, user_id, profile_id, remark, display_template, tag)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: InsertMihomoListener :exec
INSERT INTO mihomo_listeners (stack_id, kind, enabled, name, listen, port, auth, accounts)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: InsertMihomoUpstream :one
INSERT INTO mihomo_upstreams (id, stack_id, position, name, type, ref_inbound_id, raw_config)
VALUES (nullif(?, 0), ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: InsertMihomoGroup :one
INSERT INTO mihomo_groups (id, stack_id, position, name, type, proxies, url, interval, strategy)
VALUES (nullif(?, 0), ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: InsertConfigRevision :one
INSERT INTO config_revisions (created_at, actor, summary, snapshot) VALUES (?, ?, ?, ?)
RETURNING id;

-- name: ListConfigRevisions :many
SELECT id, created_at, actor, summary FROM config_revisions
ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: CountConfigRevisions :one
SELECT count(*) FROM config_revisions;

-- name: GetConfigRevision :one
SELECT * FROM config_revisions WHERE id = ?;

-- name: LatestConfigRevisionID :one
SELECT coalesce(max(id), 0) FROM config_revisions;

-- name: StopAllStacks :exec
UPDATE stacks SET desired_running = 0;
