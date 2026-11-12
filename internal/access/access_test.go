package access

import "testing"

func TestParseLine(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		want    Record
		ok      bool
	}{
		{
			name:    "vmess with timestamp",
			message: "2026/11/11 12:00:00.123456 from 1.2.3.4:5678 accepted tcp:www.google.com:443 [vmess -> out] email: alice.u7",
			want:    Record{UserID: 7, Domain: "google.com"}, ok: true,
		},
		{
			name:    "profile email without from prefix",
			message: "2026/11/11 12:00:00 1.2.3.4:5678 accepted udp:dns.google:853 [vmess >> out] email: bob.u12.p3",
			want:    Record{UserID: 12, Domain: "dns.google"}, ok: true,
		},
		{
			name:    "legacy email",
			message: "from tcp:1.2.3.4:5678 accepted tcp:img.bbc.co.uk:443 [in -> out] email: u5",
			want:    Record{UserID: 5, Domain: "bbc.co.uk"}, ok: true,
		},
		{name: "ipv4 target", message: "from 1.2.3.4:5678 accepted udp:8.8.8.8:53 [in -> out] email: u5"},
		{name: "ipv6 target", message: "from 1.2.3.4:5678 accepted tcp:[2001:DB8::1]:443 [in -> out] email: u5"},
		{
			name:    "user name with spaces and dots",
			message: "from 1.2.3.4:5678 accepted tcp:WWW.Example.COM.:443 [in -> out] email: a.b c.u9",
			want:    Record{UserID: 9, Domain: "example.com"}, ok: true,
		},
		{
			name:    "no detour",
			message: "from 1.2.3.4:5678 accepted tcp:example.org:80 email: u3",
			want:    Record{UserID: 3, Domain: "example.org"}, ok: true,
		},
		{name: "rejected", message: "from 1.2.3.4:5678 rejected tcp:example.org:80 [in -> block] email: u3"},
		{name: "relay without email", message: "from 127.0.0.1:5678 accepted tcp:example.org:443 [relay -> out]"},
		{name: "shared account", message: "from 1.2.3.4:5678 accepted tcp:example.org:443 [socks -> out] email: shared"},
		{name: "not an access line", message: "[Warning] core: Xray 25.1.1 started"},
		{name: "missing port", message: "from 1.2.3.4:5678 accepted tcp:example.org [in -> out] email: u3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseLine(tc.message)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("ParseLine() = %+v, %v; want %+v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestDomain(t *testing.T) {
	for host, want := range map[string]string{
		"www.google.com":              "google.com",
		"r3---sn-abc.googlevideo.com": "googlevideo.com",
		"google.com":                  "google.com",
		"news.bbc.co.uk":              "bbc.co.uk",
		"alice.github.io":             "alice.github.io",
		"a.b.alice.github.io":         "alice.github.io",
		"localhost":                   "localhost",
		"co.uk":                       "co.uk",
		"printer.lan":                 "printer.lan",
		"::ffff:1.2.3.4":              "",
		"8.8.8.8":                     "",
		"":                            "",
		"bad host":                    "",
	} {
		if got := Domain(host); got != want {
			t.Errorf("Domain(%q) = %q, want %q", host, got, want)
		}
	}
}
