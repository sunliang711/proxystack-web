package access

import (
	"fmt"
	"testing"

	"proxystack-web/internal/geosite"
)

func rules(kind int, values ...string) []geosite.Rule {
	out := make([]geosite.Rule, 0, len(values))
	for _, value := range values {
		out = append(out, geosite.Rule{Type: kind, Value: value})
	}
	return out
}

func TestClassifier(t *testing.T) {
	lists := []geosite.List{
		{Name: "google", Rules: rules(geosite.TypeDomain, "google.com", "googleapis.com", "gstatic.com", "1e100.net")},
		{Name: "youtube", Rules: rules(geosite.TypeDomain, "youtube.com", "googlevideo.com", "ytimg.com")},
		{Name: "instagram", Rules: rules(geosite.TypeDomain, "instagram.com", "cdninstagram.com")},
		{Name: "facebook", Rules: rules(geosite.TypeDomain, "facebook.com", "fbcdn.net", "instagram.com", "cdninstagram.com", "whatsapp.net")},
		{Name: "gmail", Rules: rules(geosite.TypeDomain, "mail.google.com")},
		{Name: "google-play", Rules: rules(geosite.TypeDomain, "play.googleapis.com", "android.clients.google.com")},
		{Name: "bilibili", Rules: rules(geosite.TypeDomain, "bilibili.com", "hdslb.com")},
		{Name: "bilibili-cdn", Rules: rules(geosite.TypeDomain, "hdslb.com")},
		{Name: "steam-cdn", Rules: rules(geosite.TypeDomain, "steamcontent.com")},
		{Name: "dropbox", Rules: append(rules(geosite.TypeDomain, "dropboxstatic.com"), rules(geosite.TypeFull, "dropbox.com")...)},
		{Name: "category-ads", Rules: rules(geosite.TypeDomain, "doubleclick.net")},
		{Name: "geolocation-!cn", Rules: rules(geosite.TypeDomain, "example.com", "x.example")},
		{Name: "google-cn", Rules: rules(geosite.TypeDomain, "googleapis.com")},
		{Name: "keywords", Rules: rules(geosite.TypePlain, "google")},
	}
	c := NewClassifier(lists, map[string]string{"example-cdn.net": "example", "fbsbx.com": "facebook"})
	for domain, want := range map[string][2]string{
		"google.com":          {"google", "google.com"},
		"play.googleapis.com": {"google", "google.com"}, // googleapis.com 是公共后缀，主域名带一级子域名
		"googlevideo.com":     {"youtube", "youtube.com"},
		"cdninstagram.com":    {"instagram", "instagram.com"}, // 同时在 facebook 中，取规则更少的 instagram
		"instagram.com":       {"instagram", "instagram.com"},
		"dropbox.com":         {"dropbox", ""}, // full 规则只匹配域名本身
		"doubleclick.net":     {"", ""},        // 汇总清单不参与
		"example.com":         {"", ""},
		"example-cdn.net":     {"example", ""},              // 手动补充
		"fbsbx.com":           {"facebook", "facebook.com"}, // 手动补充的服务名与清单同名时沿用清单的代表域名
		"unknown.org":         {"", ""},
		"hdslb.com":           {"bilibili", "bilibili.com"}, // 子清单归入包含它的上级清单
		"steamcontent.com":    {"steam-cdn", ""},            // 没有上级清单时保留子清单
	} {
		name, representative := c.Service(domain)
		if name != want[0] || representative != want[1] {
			t.Errorf("Service(%q) = %q, %q; want %q, %q", domain, name, representative, want[0], want[1])
		}
	}
	// 由公司登记的后缀下面是客户网站，不归到平台；googleapis.com 例外。
	withPlatforms := NewClassifier(append(lists,
		geosite.List{Name: "github", Rules: rules(geosite.TypeDomain, "github.com", "github.io")},
		geosite.List{Name: "aws", Rules: rules(geosite.TypeDomain, "cloudfront.net")},
	), map[string]string{"googleapis.com": "Gapi", "fbsbx.com": "FACEBOOK"})
	for domain, want := range map[string]string{
		"alice.github.io":     "",
		"d1.cloudfront.net":   "",
		"github.com":          "github",
		"play.googleapis.com": "Gapi",     // 手动补充在任一级命中都优先于下级的 geosite 规则
		"fbsbx.com":           "facebook", // 与清单同名时不区分大小写并入清单
	} {
		if name, _ := withPlatforms.Service(domain); name != want {
			t.Errorf("Service(%q) = %q, want %q", domain, name, want)
		}
	}
	// 规则过多的汇总清单（例如 china-list）不参与归类。
	huge := make([]geosite.Rule, 0, MaxServiceRules+1)
	for i := 0; i <= MaxServiceRules; i++ {
		huge = append(huge, geosite.Rule{Type: geosite.TypeDomain, Value: fmt.Sprintf("site%d.cn", i)})
	}
	if name, _ := NewClassifier([]geosite.List{{Name: "big-region", Rules: huge}}, nil).Service("site1.cn"); name != "" {
		t.Errorf("huge list must be ignored, got %q", name)
	}
	if len(ServiceLists([]geosite.List{{Name: "big-region", Rules: huge}, {Name: "x", Rules: rules(geosite.TypePlain, "x")}})) != 0 {
		t.Error("ServiceLists must drop aggregate lists and lists without domain rules")
	}

	// mail.google.com 比主域名更具体，不能把整个 google.com 归到 gmail。
	if name, _ := c.Service("google.com"); name != "google" {
		t.Fatalf("google.com = %s", name)
	}
	if !NewClassifier(nil, nil).Empty() || c.Empty() {
		t.Fatal("Empty()")
	}
}
