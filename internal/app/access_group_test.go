package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/app"
	"proxystack-web/internal/geosite"
)

func TestAccessGroupByService(t *testing.T) {
	h := newTrafficHarness(t)
	ctx := context.Background()
	alice := h.alice(t).ID
	day := h.now.Format(time.DateOnly)
	yesterday := h.now.AddDate(0, 0, -1).Format(time.DateOnly)

	// 没有 geosite 数据时不归类，每个主域名一行。
	for _, row := range []struct {
		user           int64
		day, domain    string
		count, blocked int64
	}{
		{alice, day, "google.com", 10, 0}, {alice, day, "play.googleapis.com", 5, 1}, {alice, yesterday, "google.com", 2, 0},
		{alice, day, "instagram.com", 3, 0}, {alice, day, "cdninstagram.com", 20, 0},
		{alice, day, "example.org", 1, 0}, {alice, day, "github.com", 4, 0},
		{999, day, "cdninstagram.com", 7, 0},
	} {
		_, err := h.svc.Store.DB.ExecContext(ctx, "INSERT INTO access_domain_day (user_id, day, domain, count, blocked) VALUES (?, ?, ?, ?, ?)",
			row.user, row.day, row.domain, row.count, row.blocked)
		require.NoError(t, err)
	}
	query := app.AccessQuery{From: h.now.AddDate(0, 0, -1).Add(-time.Hour), To: h.now.Add(time.Hour), Group: true}
	plain, err := h.svc.AccessUserDomains(ctx, alice, query)
	require.NoError(t, err)
	require.Len(t, plain.Domains, 6)

	// 写入 geosite 数据并加一条手动归类后按服务合并。
	data := geosite.Encode([]geosite.List{
		{Name: "google", Rules: []geosite.Rule{{Type: geosite.TypeDomain, Value: "google.com"}, {Type: geosite.TypeDomain, Value: "googleapis.com"}}},
		{Name: "instagram", Rules: []geosite.Rule{{Type: geosite.TypeDomain, Value: "instagram.com"}, {Type: geosite.TypeDomain, Value: "cdninstagram.com"}}},
		{Name: "github", Rules: []geosite.Rule{{Type: geosite.TypeDomain, Value: "github.com"}}},
		{Name: "category-dev", Rules: []geosite.Rule{{Type: geosite.TypeDomain, Value: "github.com"}}},
	})
	require.NoError(t, os.MkdirAll(h.svc.Layout.GeoDir(), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(h.svc.Layout.GeoDir(), "geosite.dat"), data, 0o640))
	doc, err := h.svc.LoadConfig(ctx)
	require.NoError(t, err)
	settings := *doc.Settings
	settings.Traffic.AccessGroups = []string{"example.org=Example"}
	_, err = h.svc.UpdateSettings(ctx, h.actor, settings)
	require.NoError(t, err)

	grouped, err := h.svc.AccessUserDomains(ctx, alice, query)
	require.NoError(t, err)
	require.Equal(t, int64(4), grouped.Rows)
	require.Equal(t, int64(6), grouped.DomainCount)
	byName := map[string]app.AccessDomainCount{}
	for _, row := range grouped.Domains {
		key := row.Domain
		if len(row.Members) > 0 {
			key = "service:" + row.Service
		}
		byName[key] = row
	}
	google := byName["service:google"]
	require.Equal(t, "google.com", google.Domain, "the representative domain carries the icon")
	require.Equal(t, int64(17), google.Count)
	require.Equal(t, int64(1), google.Blocked)
	require.Equal(t, int64(2), google.Days, "days are counted once across member domains")
	require.Len(t, google.Members, 2)
	require.Equal(t, "google.com", google.Members[0].Domain)
	instagram := byName["service:instagram"]
	require.Equal(t, int64(23), instagram.Count)
	require.Equal(t, "cdninstagram.com", instagram.Members[0].Domain)
	require.Equal(t, "github", byName["github.com"].Service, "single-domain services stay a domain row")
	require.Empty(t, byName["github.com"].Members)
	require.Equal(t, "Example", byName["example.org"].Service, "manual groups apply")
	require.Equal(t, "service:instagram", "service:"+grouped.Domains[0].Service, "rows are sorted by total")

	// 全部用户视角：服务的用户数按用户去重。
	top, err := h.svc.AccessTopDomains(ctx, query)
	require.NoError(t, err)
	require.Equal(t, int64(2), top.Domains[0].Users)
	require.Equal(t, int64(30), top.Domains[0].Count)
	ungrouped, err := h.svc.AccessTopDomains(ctx, app.AccessQuery{From: query.From, To: query.To})
	require.NoError(t, err)
	require.Equal(t, "instagram", ungrouped.Domains[0].Service)
	require.Contains(t, string(app.AccessDomainsCSV(ungrouped, false)), "domain,service,count,blocked,users\ncdninstagram.com,instagram,27,0,2\n")

	// 按服务查用户：合并全部成员域名，天数按日期去重。
	users, err := h.svc.AccessServiceUsers(ctx, "google", query)
	require.NoError(t, err)
	require.Equal(t, "google.com", users.Domain)
	require.Equal(t, []string{"google.com", "play.googleapis.com"}, users.Members)
	require.Len(t, users.Users, 1)
	require.Equal(t, int64(17), users.Users[0].Count)
	require.Equal(t, int64(2), users.Users[0].Days)
	single, err := h.svc.AccessDomainUsersData(ctx, "www.cdninstagram.com", query)
	require.NoError(t, err)
	require.Equal(t, "instagram", single.Service)
	require.True(t, strings.HasPrefix(single.Domain, "cdninstagram"))

	// geosite 文件更新后归类随之更新。
	data = geosite.Encode([]geosite.List{{Name: "meta", Rules: []geosite.Rule{{Type: geosite.TypeDomain, Value: "instagram.com"}, {Type: geosite.TypeDomain, Value: "cdninstagram.com"}}}})
	require.NoError(t, os.WriteFile(filepath.Join(h.svc.Layout.GeoDir(), "geosite.dat"), data, 0o640))
	future := time.Now().Add(time.Minute)
	require.NoError(t, os.Chtimes(filepath.Join(h.svc.Layout.GeoDir(), "geosite.dat"), future, future))
	grouped, err = h.svc.AccessUserDomains(ctx, alice, query)
	require.NoError(t, err)
	require.Equal(t, "meta", grouped.Domains[0].Service)
}
