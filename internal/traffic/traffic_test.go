package traffic_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/traffic"
	"proxystack-web/internal/xrayapi"
)

// TestParse 验证计数解析：用户和档案合并到用户用量，api 入站被忽略，未知 email 归入 unknown。
func TestParse(t *testing.T) {
	delta := traffic.Parse(7, []xrayapi.Stat{
		{Name: "user>>>u1>>>traffic>>>uplink", Value: 100},
		{Name: "user>>>u1>>>traffic>>>downlink", Value: 200},
		{Name: "user>>>u1.p3>>>traffic>>>downlink", Value: 50},
		{Name: "user>>>u9>>>traffic>>>uplink", Value: 5},
		{Name: "user>>>psw-placeholder>>>traffic>>>uplink", Value: 1},
		{Name: "inbound>>>vmess>>>traffic>>>uplink", Value: 106},
		{Name: "inbound>>>vmess>>>traffic>>>downlink", Value: 250},
		{Name: "inbound>>>api>>>traffic>>>uplink", Value: 9999},
		{Name: "outbound>>>api>>>traffic>>>uplink", Value: 9999},
		{Name: "outbound>>>egress-edge1>>>traffic>>>downlink", Value: 300},
		{Name: "user>>>u1>>>online", Value: 3},
	}, func(id int64) bool { return id == 1 })
	require.Equal(t, traffic.Bytes{Up: 100, Down: 250}, delta.Users[1])
	require.Len(t, delta.Users, 1)
	require.Equal(t, traffic.Bytes{Up: 100, Down: 200}, delta.Details[traffic.Key{StackID: 7, Scope: "user", Subject: "1"}])
	require.Equal(t, traffic.Bytes{Down: 50}, delta.Details[traffic.Key{StackID: 7, Scope: "user", Subject: "1.3"}])
	require.Equal(t, traffic.Bytes{Up: 6}, delta.Details[traffic.Key{StackID: 7, Scope: "user", Subject: "unknown"}])
	require.Equal(t, traffic.Bytes{Up: 106, Down: 250}, delta.Stack)
	_, ok := delta.Details[traffic.Key{StackID: 7, Scope: "inbound", Subject: "api"}]
	require.False(t, ok)
}

// TestPeriodStart 验证计费周期边界按设置时区计算，重置日当天 00:00 进入新周期。
func TestPeriodStart(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	at := func(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, loc) }
	require.Equal(t, at(2026, 10, 15, 0).Unix(), traffic.PeriodStart(at(2026, 11, 14, 23), loc, "monthly", 15))
	require.Equal(t, at(2026, 11, 15, 0).Unix(), traffic.PeriodStart(at(2026, 11, 15, 0), loc, "monthly", 15))
	require.Equal(t, at(2025, 12, 20, 0).Unix(), traffic.PeriodStart(at(2026, 1, 5, 8), loc, "monthly", 20))
	require.Zero(t, traffic.PeriodStart(at(2026, 1, 5, 8), loc, "none", 0))
	require.Equal(t, at(2026, 2, 20, 0), traffic.NextPeriodStart(at(2026, 1, 25, 8), loc, "monthly", 20))
	require.Equal(t, "2026-01-05", traffic.Day(at(2026, 1, 5, 23).UTC(), loc))
}

// TestParseReadableEmails 验证新旧 email 和改名前后的计数都合并到同一用户和档案。
func TestParseReadableEmails(t *testing.T) {
	delta := traffic.Parse(7, []xrayapi.Stat{
		{Name: "user>>>u7>>>traffic>>>uplink", Value: 100},
		{Name: "user>>>eagle.u7>>>traffic>>>uplink", Value: 200},
		{Name: "user>>>renamed.u7>>>traffic>>>downlink", Value: 300},
		{Name: "user>>>u7.p3>>>traffic>>>uplink", Value: 10},
		{Name: "user>>>eagle.u7.p3>>>traffic>>>downlink", Value: 20},
	}, func(id int64) bool { return id == 7 })
	require.Equal(t, traffic.Bytes{Up: 310, Down: 320}, delta.Users[7])
	require.Equal(t, traffic.Bytes{Up: 300, Down: 300}, delta.Details[traffic.Key{StackID: 7, Scope: "user", Subject: "7"}])
	require.Equal(t, traffic.Bytes{Up: 10, Down: 20}, delta.Details[traffic.Key{StackID: 7, Scope: "user", Subject: "7.3"}])
	require.Len(t, delta.Details, 2)
}
