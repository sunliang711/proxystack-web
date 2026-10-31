package configdoc_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/domain"
	"proxystack-web/internal/generator/mihomo"
	"proxystack-web/internal/generator/xray"
	"proxystack-web/internal/testutil"
)

func loadExampleDoc(t *testing.T) *configdoc.Document {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "tests", "fixtures", "example-doc.yaml"))
	require.NoError(t, err)
	doc, err := configdoc.Parse(data)
	require.NoError(t, err)
	return doc
}

func compileResolved(t *testing.T, doc *configdoc.Document) domain.StackSet {
	t.Helper()
	require.Empty(t, configdoc.Validate(doc))
	set, err := configdoc.Compile(doc)
	require.NoError(t, err)
	resolved, err := domain.ResolveStackSetUserRefs(set)
	require.NoError(t, err)
	return resolved
}

// TestExampleDocMatchesReferenceGolden 验证示例工程的等价文档生成与参考项目逐字节一致的 xray/mihomo 配置。
func TestExampleDocMatchesReferenceGolden(t *testing.T) {
	root := testutil.RepoRoot(t)
	set := compileResolved(t, loadExampleDoc(t))
	for _, name := range []string{"usa1", "usa2", "auto"} {
		xrayOut, err := xray.DumpsConfig(set, name)
		require.NoError(t, err)
		golden, err := os.ReadFile(filepath.Join(root, "tests", "golden", "xray", name+".json"))
		require.NoError(t, err)
		require.Equal(t, string(golden), xrayOut, "xray %s", name)

		mihomoOut, err := mihomo.DumpsConfig(set, name)
		require.NoError(t, err)
		golden, err = os.ReadFile(filepath.Join(root, "tests", "golden", "mihomo", name+".yaml"))
		require.NoError(t, err)
		require.Equal(t, string(golden), mihomoOut, "mihomo %s", name)
	}
}

// TestRoundTripKeepsOutput 验证 YAML 与 JSON 往返后生成物不变。
func TestRoundTripKeepsOutput(t *testing.T) {
	doc := loadExampleDoc(t)
	before := compileResolved(t, doc)
	for _, format := range []string{configdoc.FormatYAML, configdoc.FormatJSON} {
		data, err := configdoc.Marshal(doc, format)
		require.NoError(t, err)
		parsed, err := configdoc.Parse(data)
		require.NoError(t, err, string(data))
		after := compileResolved(t, parsed)
		for _, name := range []string{"usa1", "usa2", "auto"} {
			want, err := mihomo.DumpsConfig(before, name)
			require.NoError(t, err)
			got, err := mihomo.DumpsConfig(after, name)
			require.NoError(t, err)
			require.Equal(t, want, got, "%s %s", format, name)
		}
	}
}

// TestValidateMapsDomainIssuesToFields 验证跨 stack 端口冲突定位到文档字段。
func TestValidateMapsDomainIssuesToFields(t *testing.T) {
	doc := loadExampleDoc(t)
	doc.Stacks[2].Xray.Inbounds[0].Port = 24001
	issues := configdoc.Validate(doc)
	require.NotEmpty(t, issues)
	require.Equal(t, "stacks[2].xray.inbounds[0].port", issues[0].Path)
}

// TestRenameAndMembers 验证改名同步引用、成员增删同步上游和代理组。
func TestRenameAndMembers(t *testing.T) {
	doc := loadExampleDoc(t)
	configdoc.RenameStack(doc, "usa1", "us-east")
	doc.Stacks[1].Name = "us-east"
	require.Equal(t, "us-east", doc.Stacks[0].Mihomo.Upstreams[0].Stack)
	require.Empty(t, configdoc.Validate(doc))

	auto := &doc.Stacks[0]
	configdoc.SetMembers(auto, []string{"usa2"})
	require.Len(t, auto.Mihomo.Upstreams, 1)
	require.Equal(t, []string{"usa2-local"}, auto.Mihomo.Groups[0].Proxies)
	require.Equal(t, []string{"AutoProxy", "BalanceProxy", "usa2-local", "DIRECT"}, auto.Mihomo.Groups[2].Proxies)
	configdoc.SetMembers(auto, []string{"usa2", "us-east"})
	require.Equal(t, []string{"AutoProxy", "BalanceProxy", "usa2-local", "us-east-local", "DIRECT"}, auto.Mihomo.Groups[2].Proxies)
	require.Empty(t, configdoc.Validate(doc))
}

// TestNewStackAndClone 验证模板生成的 stack 可直接通过校验，克隆后端口重新分配。
func TestNewStackAndClone(t *testing.T) {
	doc := loadExampleDoc(t)
	settings := *doc.Settings
	edge, err := configdoc.NewStack(doc, settings, configdoc.NewStackOptions{Template: configdoc.TemplatePair, Name: "jp1"})
	require.NoError(t, err)
	doc.Stacks = append(doc.Stacks, edge)
	require.Empty(t, configdoc.Validate(doc))

	auto, err := configdoc.NewStack(doc, settings, configdoc.NewStackOptions{Template: configdoc.TemplateLoadBalance, Name: "lb", Members: []string{"jp1", "usa1"}})
	require.NoError(t, err)
	doc.Stacks = append(doc.Stacks, auto)
	require.Empty(t, configdoc.Validate(doc))

	clone := configdoc.CloneStack(&doc.Stacks[1], "usa3")
	require.NoError(t, configdoc.AllocateStackPorts(doc, settings, &clone, nil))
	doc.Stacks = append(doc.Stacks, clone)
	require.Empty(t, configdoc.Validate(doc))
	require.Equal(t, "usa3 relay socks", clone.Xray.Inbounds[0].Remark)
}
