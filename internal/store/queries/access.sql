-- name: AddAccessDomain :exec
INSERT INTO access_domain_day (user_id, day, domain, count, blocked) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (user_id, day, domain) DO UPDATE SET count = count + excluded.count, blocked = blocked + excluded.blocked;

-- name: GetAccessCursor :one
SELECT * FROM access_cursor WHERE id = 1;

-- name: PutAccessCursor :exec
INSERT INTO access_cursor (id, cursor, at) VALUES (1, ?, ?)
ON CONFLICT (id) DO UPDATE SET cursor = excluded.cursor, at = excluded.at;

-- name: DeleteAccessCursor :exec
DELETE FROM access_cursor;

-- name: DeleteAccessBefore :exec
DELETE FROM access_domain_day WHERE day < ?;

-- name: ListAccessUserDomains :many
SELECT domain, CAST(SUM(count) AS INTEGER) AS count, CAST(SUM(blocked) AS INTEGER) AS blocked,
       COUNT(DISTINCT day) AS days
FROM access_domain_day
WHERE user_id = sqlc.arg(user_id) AND day >= sqlc.arg(from_day) AND day <= sqlc.arg(to_day)
GROUP BY domain
ORDER BY SUM(count) + SUM(blocked) DESC, domain
LIMIT sqlc.arg(row_limit);

-- name: ListAccessDomainUsers :many
SELECT user_id, CAST(SUM(count) AS INTEGER) AS count, CAST(SUM(blocked) AS INTEGER) AS blocked,
       COUNT(DISTINCT day) AS days
FROM access_domain_day
WHERE domain = sqlc.arg(domain) AND day >= sqlc.arg(from_day) AND day <= sqlc.arg(to_day)
GROUP BY user_id
ORDER BY SUM(count) + SUM(blocked) DESC, user_id;

-- name: ListAccessTopDomains :many
SELECT domain, CAST(SUM(count) AS INTEGER) AS count, CAST(SUM(blocked) AS INTEGER) AS blocked,
       COUNT(DISTINCT user_id) AS users
FROM access_domain_day
WHERE day >= sqlc.arg(from_day) AND day <= sqlc.arg(to_day)
GROUP BY domain
ORDER BY SUM(count) + SUM(blocked) DESC, domain
LIMIT sqlc.arg(row_limit);

-- name: CountAccessDomains :one
SELECT COUNT(DISTINCT domain) AS domains, CAST(COALESCE(SUM(count), 0) AS INTEGER) AS count,
       CAST(COALESCE(SUM(blocked), 0) AS INTEGER) AS blocked
FROM access_domain_day
WHERE day >= sqlc.arg(from_day) AND day <= sqlc.arg(to_day);

-- name: ListAccessDomainUserPairs :many
SELECT domain, user_id, CAST(SUM(count) AS INTEGER) AS count, CAST(SUM(blocked) AS INTEGER) AS blocked
FROM access_domain_day
WHERE day >= sqlc.arg(from_day) AND day <= sqlc.arg(to_day)
GROUP BY domain, user_id;

-- name: ListAccessUserDomainDays :many
SELECT domain, day, count, blocked
FROM access_domain_day
WHERE user_id = sqlc.arg(user_id) AND day >= sqlc.arg(from_day) AND day <= sqlc.arg(to_day);

-- name: ListAccessDomainsUserDays :many
SELECT user_id, day, CAST(SUM(count) AS INTEGER) AS count, CAST(SUM(blocked) AS INTEGER) AS blocked
FROM access_domain_day
WHERE day >= sqlc.arg(from_day) AND day <= sqlc.arg(to_day) AND domain IN (sqlc.slice(domains))
GROUP BY user_id, day;

-- name: ListAccessDomainNames :many
SELECT DISTINCT domain FROM access_domain_day
WHERE day >= sqlc.arg(from_day) AND day <= sqlc.arg(to_day);
