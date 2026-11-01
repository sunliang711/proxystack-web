-- +goose Up
-- 将旧默认计划升级为两分钟；保留自定义计划、启用状态和查询超时。
UPDATE jobs
SET schedule = CASE WHEN schedule = '* * * * *' THEN '*/2 * * * *' ELSE schedule END,
    params = json_set(json_remove(params, '$.failure_threshold'), '$.retry_count', COALESCE(json_extract(params, '$.retry_count'), 3))
WHERE kind = 'egress_check';

-- 旧的跨周期计数不再参与判断，已经确认的故障与通知编号继续保留。
UPDATE stack_egress_health
SET failures = 0,
    failure_since = CASE WHEN alarmed = 0 THEN 0 ELSE failure_since END,
    status = CASE WHEN status = 'failing' THEN 'unknown' ELSE status END;

-- +goose Down
UPDATE jobs
SET schedule = CASE WHEN schedule = '*/2 * * * *' THEN '* * * * *' ELSE schedule END,
    params = json_set(json_remove(params, '$.retry_count'), '$.failure_threshold', 2)
WHERE kind = 'egress_check';
