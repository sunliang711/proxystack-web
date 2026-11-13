-- +goose Up
-- 图标获取改为首页截断读取、每个请求单独限时并先试 www. 主机，之前得到的「没有图标」结论作废，下次显示时重新获取。
DELETE FROM favicons WHERE ok = 0;

-- +goose Down
SELECT 1;
