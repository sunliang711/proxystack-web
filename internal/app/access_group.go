package app

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"proxystack-web/internal/access"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/geosite"
)

// geositeState 缓存解析后的 geosite 清单和由它与手动归类组成的归类；geo 数据更新（文件变化）或手动归类修改后重建。
type geositeState struct {
	mu         sync.Mutex
	modTime    time.Time
	size       int64
	loaded     bool
	lists      []geosite.List
	groupsKey  string
	classifier *access.Classifier
}

// accessClassifier 返回当前的域名归类。没有 geosite 数据或解析失败时只使用手动归类。
func (s *Service) accessClassifier(settings configdoc.Settings) *access.Classifier {
	path := filepath.Join(s.Layout.GeoDir(), "geosite.dat")
	var modTime time.Time
	var size int64
	if info, err := os.Stat(path); err == nil {
		modTime, size = info.ModTime(), info.Size()
	}
	groupsKey := strings.Join(settings.Traffic.AccessGroups, "\n")
	state := &s.geosite
	state.mu.Lock()
	defer state.mu.Unlock()
	fileChanged := !state.loaded || !modTime.Equal(state.modTime) || size != state.size
	if fileChanged {
		state.lists = nil
		if size > 0 {
			if data, err := os.ReadFile(path); err != nil {
				slog.Warn("geosite", "err", err)
			} else if lists, err := geosite.Parse(data); err != nil {
				slog.Warn("geosite", "path", path, "err", err)
			} else {
				// 只缓存参与归类的清单和规则：汇总清单、关键字和正则规则占大部分内存但用不到。
				state.lists = access.ServiceLists(lists)
			}
		}
		state.modTime, state.size, state.loaded = modTime, size, true
	}
	if fileChanged || state.classifier == nil || groupsKey != state.groupsKey {
		state.classifier = access.NewClassifier(state.lists, configdoc.AccessGroupMap(settings.Traffic.AccessGroups))
		state.groupsKey = groupsKey
	}
	return state.classifier
}

// accessFact 是一个主域名在某个维度（用户或日期）上的计数，用于按服务汇总时去重计算用户数或天数。
type accessFact struct {
	domain  string
	key     string
	count   int64
	blocked int64
}

// accessGroup 是汇总中的一个域名或服务。
type accessGroup struct {
	row  AccessDomainCount
	keys map[string]bool
}

func (g *accessGroup) add(count, blocked int64, key string) {
	g.row.Count += count
	g.row.Blocked += blocked
	g.keys[key] = true
}

func accessTotal(row AccessDomainCount) int64 { return row.Count + row.Blocked }

func sortAccessRows(rows []AccessDomainCount) {
	sort.SliceStable(rows, func(i, j int) bool {
		if a, b := accessTotal(rows[i]), accessTotal(rows[j]); a != b {
			return a > b
		}
		return rows[i].Domain < rows[j].Domain
	})
}

// groupAccess 把计数汇总成排行行。group 为 false 时每个主域名一行；为 true 时同一服务的多个主域名合并为一行，
// Members 列出各主域名，只有一个主域名的服务仍按主域名显示。distinct 把去重后的用户数或天数写进行里，
// decorate 为主域名行补充黑名单状态等信息。
func groupAccess(facts []accessFact, classifier *access.Classifier, group bool,
	distinct func(row *AccessDomainCount, n int64), decorate func(row *AccessDomainCount)) []AccessDomainCount {
	domains := map[string]*accessGroup{}
	for _, fact := range facts {
		item, ok := domains[fact.domain]
		if !ok {
			item = &accessGroup{row: AccessDomainCount{Domain: fact.domain}, keys: map[string]bool{}}
			item.row.Service, _ = classifier.Service(fact.domain)
			domains[fact.domain] = item
		}
		item.add(fact.count, fact.blocked, fact.key)
	}
	domainRow := func(item *accessGroup) AccessDomainCount {
		row := item.row
		distinct(&row, int64(len(item.keys)))
		decorate(&row)
		return row
	}
	rows := []AccessDomainCount{}
	if !group {
		for _, item := range domains {
			rows = append(rows, domainRow(item))
		}
		sortAccessRows(rows)
		return rows
	}
	services := map[string][]*accessGroup{}
	for _, item := range domains {
		if item.row.Service == "" {
			rows = append(rows, domainRow(item))
			continue
		}
		services[item.row.Service] = append(services[item.row.Service], item)
	}
	for name, members := range services {
		if len(members) == 1 {
			rows = append(rows, domainRow(members[0]))
			continue
		}
		service := &accessGroup{row: AccessDomainCount{Service: name, Members: []AccessDomainCount{}}, keys: map[string]bool{}}
		for _, member := range members {
			service.row.Count += member.row.Count
			service.row.Blocked += member.row.Blocked
			for key := range member.keys {
				service.keys[key] = true
			}
			service.row.Members = append(service.row.Members, domainRow(member))
		}
		sortAccessRows(service.row.Members)
		// 服务行的 Domain 是用于显示图标的代表域名：清单给出的（例如 instagram.com），没有时取次数最多的主域名。
		_, representative := classifier.Service(members[0].row.Domain)
		if representative == "" {
			representative = service.row.Members[0].Domain
		}
		service.row.Domain = representative
		distinct(&service.row, int64(len(service.keys)))
		rows = append(rows, service.row)
	}
	sortAccessRows(rows)
	return rows
}
