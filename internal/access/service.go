package access

import (
	"slices"
	"sort"
	"strings"

	"golang.org/x/net/publicsuffix"

	"proxystack-web/internal/geosite"
)

// Classifier 把主域名归到它所属的服务（网站），例如 cdninstagram.com 归到 instagram、googlevideo.com 归到 youtube。
// 归类依据是 geosite 中按服务划分的域名清单，加上管理员手动补充的对应关系；手动补充优先。
type Classifier struct {
	overrides map[string]string
	domains   map[string][]int
	full      map[string][]int
	lists     []serviceInfo
	byName    map[string]int
}

type serviceInfo struct {
	name string
	size int
	// representative 是用于显示图标的代表域名，例如 google 清单的 google.com；清单中没有「名称.com」这类域名时为空。
	representative string
}

// MaxServiceRules 是单个服务清单的规则数上限。真实的服务清单在两千条以内（最大的 apple 约 1800 条），
// 超过上限的是按地区或用途汇总的清单（例如 china-list、cn），不参与归类，否则大量域名会并进一个「服务」。
const MaxServiceRules = 3000

// firstPartySuffixes 是公共后缀列表中由公司登记、但下面的子域名都是该公司自己服务的后缀，归类时可以越过它们向上查找。
// 其余由公司登记的后缀（例如 github.io、workers.dev、cloudfront.net）下面是各个客户的网站，归类到平台会掩盖用户
// 实际访问的网站，所以查找到它们为止。
var firstPartySuffixes = map[string]bool{"googleapis.com": true}

// ServiceList 判断 geosite 清单名是否表示单个服务。按用途、地区或可访问性划分的汇总清单不参与归类，
// 例如 category-ads、geolocation-!cn、cn、google-cn、china-list、tracker；规则数超过 MaxServiceRules 的清单也不参与。
func ServiceList(name string) bool {
	if name == "" || strings.ContainsAny(name, "!@") {
		return false
	}
	for _, prefix := range []string{"category-", "geolocation-", "tld-"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	if strings.HasSuffix(name, "-cn") {
		return false
	}
	switch name {
	case "cn", "private", "gfw", "greatfire", "ads", "win-spy", "win-update", "win-extra", "china-list", "tracker", "connectivity-check":
		return false
	}
	return true
}

// NewClassifier 用 geosite 清单和手动补充的对应关系（域名 → 服务名，域名匹配它本身及全部子域名）建立归类。
func NewClassifier(lists []geosite.List, overrides map[string]string) *Classifier {
	c := &Classifier{overrides: map[string]string{}, domains: map[string][]int{}, full: map[string][]int{}, byName: map[string]int{}}
	for domain, service := range overrides {
		c.overrides[strings.ToLower(domain)] = strings.TrimSpace(service)
	}
	for _, list := range ServiceLists(lists) {
		if _, ok := c.byName[list.Name]; ok {
			continue
		}
		index := len(c.lists)
		info := serviceInfo{name: list.Name, size: len(list.Rules)}
		own := map[string]bool{}
		for _, rule := range list.Rules {
			if rule.Type == geosite.TypeDomain {
				c.domains[rule.Value] = append(c.domains[rule.Value], index)
				own[rule.Value] = true
			} else {
				c.full[rule.Value] = append(c.full[rule.Value], index)
			}
		}
		for _, tld := range []string{".com", ".net", ".org", ".tv", ".io", ".me", ".cn"} {
			if own[list.Name+tld] {
				info.representative = list.Name + tld
				break
			}
		}
		c.lists = append(c.lists, info)
		c.byName[list.Name] = index
	}
	return c
}

// ServiceLists 只保留参与归类的清单和其中的域名、完整域名规则（关键字和正则不用于归类），用于缓存时减少内存。
func ServiceLists(lists []geosite.List) []geosite.List {
	out := []geosite.List{}
	for _, list := range lists {
		if !ServiceList(list.Name) {
			continue
		}
		rules := make([]geosite.Rule, 0, len(list.Rules))
		for _, rule := range list.Rules {
			if rule.Type == geosite.TypeDomain || rule.Type == geosite.TypeFull {
				rules = append(rules, rule)
			}
		}
		if len(rules) == 0 || len(rules) > MaxServiceRules {
			continue
		}
		out = append(out, geosite.List{Name: list.Name, Rules: rules})
	}
	return out
}

// levels 返回归类时依次查找的各级域名：主域名本身及其上级，到 com、co.uk 这类顶级后缀为止；遇到由公司登记的后缀时，
// 只有 firstPartySuffixes 中的可以继续越过。
func levels(domain string) []string {
	out := []string{}
	for current := domain; current != ""; {
		suffix, icann := publicsuffix.PublicSuffix(current)
		if suffix == current && (icann || current != domain && !firstPartySuffixes[current]) {
			break
		}
		out = append(out, current)
		dot := strings.IndexByte(current, '.')
		if dot < 0 {
			break
		}
		current = current[dot+1:]
	}
	return out
}

// Service 返回主域名所属的服务名和代表域名；不属于任何服务时返回空串。
// 手动补充优先：任一级命中手动补充即采用。否则按 geosite 清单从主域名本身逐级往上查找，最先命中的一级生效，
// 同一级被多个清单包含时取规则最少（最具体）的清单，例如 cdninstagram.com 同时在 instagram 和 facebook 中，归到 instagram。
func (c *Classifier) Service(domain string) (name, representative string) {
	if c == nil || domain == "" {
		return "", ""
	}
	path := levels(domain)
	for _, current := range path {
		if service, ok := c.overrides[current]; ok && service != "" {
			// 与清单同名（不区分大小写）时并入该清单，使用清单的名称和代表域名。
			if index, ok := c.byName[strings.ToLower(service)]; ok {
				return c.lists[index].name, c.lists[index].representative
			}
			return service, ""
		}
	}
	for _, current := range path {
		candidates := append([]int{}, c.domains[current]...)
		if current == domain {
			candidates = append(candidates, c.full[current]...)
		}
		if len(candidates) == 0 {
			continue
		}
		sort.Slice(candidates, func(i, j int) bool {
			a, b := c.lists[candidates[i]], c.lists[candidates[j]]
			if a.size != b.size {
				return a.size < b.size
			}
			return a.name < b.name
		})
		best := candidates[0]
		// 子清单归入同名前缀的上级清单：google-play 归到 google、bilibili-cdn 归到 bilibili，
		// 前提是上级清单也包含这个域名。
		for {
			dash := strings.LastIndexByte(c.lists[best].name, '-')
			if dash <= 0 {
				break
			}
			parent, ok := c.byName[c.lists[best].name[:dash]]
			if !ok || !c.covers(parent, domain) {
				break
			}
			best = parent
		}
		return c.lists[best].name, c.lists[best].representative
	}
	return "", ""
}

// covers 判断清单是否包含该域名（它本身或任一上级域名的域名规则，或与它相同的完整域名规则）。
func (c *Classifier) covers(list int, domain string) bool {
	if slices.Contains(c.full[domain], list) {
		return true
	}
	for current := domain; current != ""; {
		if slices.Contains(c.domains[current], list) {
			return true
		}
		dot := strings.IndexByte(current, '.')
		if dot < 0 {
			break
		}
		current = current[dot+1:]
	}
	return false
}

// Empty 判断归类是否没有任何依据（没有 geosite 数据也没有手动补充）。
func (c *Classifier) Empty() bool { return c == nil || (len(c.lists) == 0 && len(c.overrides) == 0) }
