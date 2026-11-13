package configdoc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeBlockedDomain(t *testing.T) {
	for input, want := range map[string]string{
		"Example.COM":            "example.com",
		"  example.com.  ":       "example.com",
		"domain:Ads.Example":     "ads.example",
		"FULL:WWW.Example.com":   "full:www.example.com",
		"full: www.example.com ": "full:www.example.com",
		"cn":                     "cn",
		"xn--fiqs8s":             "xn--fiqs8s",
	} {
		got, ok := NormalizeBlockedDomain(input)
		require.True(t, ok, input)
		require.Equal(t, want, got, input)
	}
	for _, input := range []string{"", "full:", "1.2.3.4", "[2001:db8::1]", "a..b", "-bad.com", "bad-.com", "has space.com", "a,b.com", "例子.中国", "http://example.com", "*.example.com"} {
		_, ok := NormalizeBlockedDomain(input)
		require.False(t, ok, input)
	}
}

func TestBlockedDomainsNormalizeAndValidate(t *testing.T) {
	doc := &Document{Settings: new(DefaultSettings()), Users: []User{NewUser("alice")}}
	doc.Settings.Traffic.BlockedDomains = []string{"Example.com", " example.com ", "", "full:WWW.a.org"}
	doc.Users[0].BlockedDomains = []string{"domain:B.org", "1.2.3.4"}
	doc.Normalize()

	require.Equal(t, []string{"example.com", "full:www.a.org"}, doc.Settings.Traffic.BlockedDomains)
	require.Equal(t, []string{"b.org", "1.2.3.4"}, doc.Users[0].BlockedDomains)
	var paths []string
	for _, issue := range Validate(doc) {
		paths = append(paths, issue.Path)
	}
	require.Contains(t, paths, "users[0].blocked_domains")
	require.NotContains(t, paths, "settings.traffic.blocked_domains")
}

func TestBlockedDomainMatchesAndXrayRule(t *testing.T) {
	require.True(t, BlockedDomainMatches("example.com", "example.com"))
	require.True(t, BlockedDomainMatches("example.com", "WWW.Example.com."))
	require.False(t, BlockedDomainMatches("example.com", "badexample.com"))
	require.True(t, BlockedDomainMatches("full:www.example.com", "www.example.com"))
	require.False(t, BlockedDomainMatches("full:www.example.com", "a.www.example.com"))
	require.Equal(t, "domain:example.com", XrayDomainRule("example.com"))
	require.Equal(t, "full:www.example.com", XrayDomainRule("full:www.example.com"))
}

func TestCompileBlockedDomains(t *testing.T) {
	doc := &Document{Settings: new(DefaultSettings()), Users: []User{NewUser("alice"), NewUser("bob")}}
	doc.Settings.Traffic.BlockedDomains = []string{"example.com", "full:www.a.org"}
	doc.Users[1].BlockedDomains = []string{"video.example"}

	set, err := Compile(doc)

	require.NoError(t, err)
	require.Equal(t, []string{"domain:example.com", "full:www.a.org"}, set.Config.BlockedDomains)
	require.Equal(t, map[string][]string{"bob": {"domain:video.example"}}, set.Config.UserBlockedDomains)
}
