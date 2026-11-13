-- +goose Up
-- 已发出告警、尚未恢复的健康检查项。check_key 是检查项的告警标识，since 是确认异常的时间，
-- status 是最近一次告警时的结论（warn 或 fail），detail 是确认异常时的检查结果。
-- 重启后据此继续判断加重与恢复，避免重复告警或漏发恢复通知。
CREATE TABLE health_alerts (
    check_key TEXT    PRIMARY KEY,
    since     INTEGER NOT NULL,
    status    TEXT    NOT NULL,
    detail    TEXT    NOT NULL DEFAULT ''
) WITHOUT ROWID;

-- +goose Down
DROP TABLE health_alerts;
