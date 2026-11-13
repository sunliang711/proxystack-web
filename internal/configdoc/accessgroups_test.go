package configdoc

import (
	"reflect"
	"testing"
)

func TestAccessGroups(t *testing.T) {
	got := normalizeAccessGroups([]string{" Example-CDN.com. = Example ", "", "example-cdn.com=Example", "bad", "x.com=", nil2()})
	want := []string{"example-cdn.com=Example", "bad", "x.com="}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalize = %q, want %q", got, want)
	}
	issues := &Issues{}
	checkAccessGroups(issues, "settings.traffic.access_groups", got)
	if len(*issues) != 2 {
		t.Fatalf("issues = %v", *issues)
	}
	if _, _, err := ParseAccessGroup("alice.github.io=Alice"); err != nil {
		t.Errorf("a site under a company-registered suffix is a registrable domain: %v", err)
	}
	if m := AccessGroupMap([]string{"a.com=A", "a.com=B", "bad"}); !reflect.DeepEqual(m, map[string]string{"a.com": "B"}) {
		t.Fatalf("map = %v", m)
	}
	for _, entry := range []string{"1.2.3.4=x", "a.com=x=y", "a.com=" + string(make([]rune, 41)), "www.example.com=x", "co.uk=x", "github.io=x"} {
		if _, _, err := ParseAccessGroup(entry); err == nil {
			t.Errorf("ParseAccessGroup(%q) should fail", entry)
		}
	}
}

func nil2() string { return "   " }
