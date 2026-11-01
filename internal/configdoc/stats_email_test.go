package configdoc_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/generator/xray"
)

// TestRenderedStatsEmails 验证真实文档中的用户和额外档案在生成配置与热添加片段中使用相同可读标识。
func TestRenderedStatsEmails(t *testing.T) {
	for _, protocol := range []string{"vmess", "shadowsocks"} {
		t.Run(protocol, func(t *testing.T) {
			doc := loadExampleDoc(t)
			doc.Users[0].ID = 7
			doc.Users[0].Profiles = []configdoc.Profile{{
				ID: 3, Name: "mobile", UUID: "22222222-2222-4222-8222-222222222222", SSPassword: "demo-mobile-password",
			}}
			stack := &doc.Stacks[doc.StackByName("usa1")]
			inbound := &stack.Xray.Inbounds[1]
			inbound.Protocol = protocol
			if protocol == "shadowsocks" {
				inbound.Method = "aes-128-gcm"
				inbound.Password = "demo-inbound-password"
			}
			inbound.Users[0].Profile = "mobile"
			set := compileResolved(t, doc)
			output, err := xray.DumpsConfig(set, "usa1")
			require.NoError(t, err)
			var generated struct {
				Inbounds []struct {
					Settings struct {
						Clients []struct {
							Email string `json:"email"`
						} `json:"clients"`
					} `json:"settings"`
				} `json:"inbounds"`
			}
			require.NoError(t, json.Unmarshal([]byte(output), &generated))
			require.Equal(t, "alice.u7.p3", generated.Inbounds[1].Settings.Clients[0].Email)
			resolved := set.Stacks[doc.StackByName("usa1")].Xray.Inbounds[1]
			patch, err := xray.DumpsInboundUserPatch(resolved, resolved.Users[0])
			require.NoError(t, err)
			require.Contains(t, patch, `"email": "alice.u7.p3"`)
		})
	}
}
