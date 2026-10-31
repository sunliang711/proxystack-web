package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseEgress 验证各数据源响应中的地址、地区、国家代码和运营商都能取出。
func TestParseEgress(t *testing.T) {
	cases := []struct {
		name string
		body string
		want EgressInfo
	}{
		{
			name: "ipinfo",
			body: `{"ip":"203.0.113.10","city":"Tokyo","region":"Tokyo","country":"JP","org":"AS2516 KDDI CORPORATION"}`,
			want: EgressInfo{IP: "203.0.113.10", Region: "JP Tokyo Tokyo", City: "Tokyo", Country: "JP", Org: "AS2516 KDDI CORPORATION"},
		},
		{
			name: "ifconfig.co",
			body: `{"ip":"2001:db8::1","country":"Germany","country_iso":"DE","city":"Frankfurt","asn":"AS24940","asn_org":"Hetzner Online GmbH"}`,
			want: EgressInfo{IP: "2001:db8::1", Region: "Germany Frankfurt", City: "Frankfurt", Country: "DE", Org: "AS24940 Hetzner Online GmbH"},
		},
		{
			name: "ifconfig.me",
			body: `{"ip_addr":"2001:db8::2","remote_host":"unavailable"}`,
			want: EgressInfo{IP: "2001:db8::2"},
		},
		{
			name: "ipip plain text",
			body: "当前 IP：198.51.100.7  来自于：中国 广东 深圳  电信\n",
			want: EgressInfo{IP: "198.51.100.7", Region: "中国 广东 深圳  电信"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, parseEgress([]byte(tc.body)))
		})
	}
}
