-- name: CountAdmins :one
SELECT count(*) FROM admins;

-- name: CreateAdmin :one
INSERT INTO admins (username, password_hash, must_change_password, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetAdminByID :one
SELECT * FROM admins WHERE id = ?;

-- name: GetAdminByUsername :one
SELECT * FROM admins WHERE username = ?;

-- name: ListAdmins :many
SELECT * FROM admins ORDER BY id;

-- name: SetAdminPassword :exec
UPDATE admins
SET password_hash = ?, must_change_password = ?, failed_logins = 0, locked_until = 0, updated_at = ?
WHERE id = ?;

-- name: ClearAdminTOTP :exec
UPDATE admins SET totp_secret = '', updated_at = ? WHERE id = ?;

-- name: RecordLoginFailure :exec
UPDATE admins SET failed_logins = failed_logins + 1, locked_until = ? WHERE id = ?;

-- name: ResetLoginFailures :exec
UPDATE admins SET failed_logins = 0, locked_until = 0 WHERE id = ?;

-- name: DeleteAllAdmins :exec
DELETE FROM admins;

-- name: RestoreAdmin :exec
INSERT INTO admins (id, username, password_hash, must_change_password, totp_secret, failed_logins, locked_until, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);
