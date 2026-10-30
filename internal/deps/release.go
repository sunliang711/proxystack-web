// Package deps 下载、校验并解出 xray、mihomo、geo 数据和管理服务自身的发布包。
//
// 版本解析、资产命名、归档解压和路径安全检查沿用参考项目的安装逻辑。
package deps

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/cpu"
)

// 组件名。
const (
	Xray   = "xray"
	Mihomo = "mihomo"
	Geo    = "geo"
	Self   = "proxystack-web"
)

// GitHubAPI 是 GitHub API 的地址，测试时替换为本地服务。
var GitHubAPI = "https://api.github.com"

// SelfRepo 是管理服务发布包所在的 GitHub 仓库。
var SelfRepo = "eagle/proxystack-web"

// Repo 返回组件的 GitHub 仓库。
func Repo(component string) string {
	switch component {
	case Xray:
		return "XTLS/Xray-core"
	case Mihomo:
		return "MetaCubeX/mihomo"
	case Geo:
		return "Loyalsoldier/v2ray-rules-dat"
	case Self:
		return SelfRepo
	}
	return ""
}

// Release 是一个上游发布版本。
type Release struct {
	Tag         string    `json:"tag"`
	Name        string    `json:"name"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Notes       string    `json:"notes"`
	Assets      []Asset   `json:"-"`
}

// Asset 是发布版本中的一个文件。
type Asset struct {
	Name string
	URL  string
	Size int64
}

type githubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	PublishedAt time.Time `json:"published_at"`
	Body        string    `json:"body"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func (r githubRelease) toRelease() Release {
	release := Release{Tag: r.TagName, Name: r.Name, Prerelease: r.Prerelease, PublishedAt: r.PublishedAt, Notes: r.Body}
	for _, asset := range r.Assets {
		release.Assets = append(release.Assets, Asset{Name: asset.Name, URL: asset.URL, Size: asset.Size})
	}
	return release
}

// Releases 列出组件最近的发布版本（不含草稿），includePre 为 false 时去掉预发布版本。
func Releases(ctx context.Context, fetcher Fetcher, component string, includePre bool) ([]Release, error) {
	data, err := fetcher.Get(ctx, GitHubAPI+"/repos/"+Repo(component)+"/releases?per_page=30")
	if err != nil {
		return nil, err
	}
	var raw []githubRelease
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse release list: %w", err)
	}
	releases := []Release{}
	for _, item := range raw {
		if item.Draft || (item.Prerelease && !includePre) {
			continue
		}
		releases = append(releases, item.toRelease())
	}
	return releases, nil
}

// FindRelease 读取指定版本的发布信息；version 为空或 latest 时取最新稳定版。
// 版本号前缀 v 可有可无。
func FindRelease(ctx context.Context, fetcher Fetcher, component, version string) (Release, error) {
	base := GitHubAPI + "/repos/" + Repo(component) + "/releases/"
	if version == "" || version == "latest" {
		return fetchRelease(ctx, fetcher, base+"latest")
	}
	trimmed := strings.TrimPrefix(strings.TrimPrefix(version, "v"), "V")
	candidates := []string{version}
	for _, tag := range []string{"v" + trimmed, trimmed} {
		if tag != version {
			candidates = append(candidates, tag)
		}
	}
	var failures []string
	for _, tag := range candidates {
		release, err := fetchRelease(ctx, fetcher, base+"tags/"+url.PathEscape(tag))
		if err == nil {
			return release, nil
		}
		failures = append(failures, err.Error())
	}
	return Release{}, fmt.Errorf("release %s not found: %s", version, strings.Join(failures, "; "))
}

func fetchRelease(ctx context.Context, fetcher Fetcher, endpoint string) (Release, error) {
	data, err := fetcher.Get(ctx, endpoint)
	if err != nil {
		return Release{}, err
	}
	var raw githubRelease
	if err := json.Unmarshal(data, &raw); err != nil {
		return Release{}, fmt.Errorf("parse release: %w", err)
	}
	return raw.toRelease(), nil
}

// mihomo 构建变体。
const (
	VariantAuto       = "auto"
	VariantStandard   = "standard"
	VariantCompatible = "compatible"
)

// ResolveVariant 把 auto 解析为具体变体：amd64 上 CPU 不支持 x86-64-v3（AVX2 等）时使用 compatible。
func ResolveVariant(variant, goarch string) string {
	if variant != VariantAuto && variant != "" {
		return variant
	}
	if goarch == "amd64" && !(cpu.X86.HasAVX2 && cpu.X86.HasBMI2 && cpu.X86.HasFMA) {
		return VariantCompatible
	}
	return VariantStandard
}

var xrayPlatforms = map[string]string{
	"linux/386": "linux-32", "linux/amd64": "linux-64", "linux/arm": "linux-arm32-v7a", "linux/arm64": "linux-arm64-v8a",
	"linux/riscv64": "linux-riscv64", "linux/s390x": "linux-s390x", "linux/loong64": "linux-loong64",
	"darwin/amd64": "macos-64", "darwin/arm64": "macos-arm64-v8a",
}

// SelectAsset 选出与平台匹配的资产及其摘要文件（没有摘要时 digest 为 nil）。
func SelectAsset(component string, release Release, goos, goarch, variant string) (Asset, *Asset, error) {
	var want func(name string) bool
	switch component {
	case Xray:
		platform, ok := xrayPlatforms[goos+"/"+goarch]
		if !ok {
			return Asset{}, nil, fmt.Errorf("xray has no release for %s/%s", goos, goarch)
		}
		want = func(name string) bool { return name == "Xray-"+platform+".zip" }
	case Mihomo:
		compatible := ResolveVariant(variant, goarch) == VariantCompatible && goarch == "amd64"
		prefix := fmt.Sprintf("mihomo-%s-%s-", goos, goarch)
		if goarch == "arm" {
			prefix = fmt.Sprintf("mihomo-%s-armv7-", goos)
		}
		want = func(name string) bool {
			if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".gz") || strings.Contains(name, "-go1") {
				return false
			}
			rest := strings.TrimPrefix(name, prefix)
			if compatible {
				return strings.HasPrefix(rest, "compatible-")
			}
			return strings.HasPrefix(rest, "v") || strings.HasPrefix(rest, release.Tag)
		}
	case Geo:
		want = func(name string) bool { return name == "geoip.dat" || name == "geosite.dat" }
	case Self:
		want = func(name string) bool { return name == fmt.Sprintf("proxystack-web-%s-%s", goos, goarch) }
	default:
		return Asset{}, nil, fmt.Errorf("unknown component: %s", component)
	}
	names := make([]string, 0, len(release.Assets))
	for _, asset := range release.Assets {
		names = append(names, asset.Name)
		if !want(asset.Name) {
			continue
		}
		return asset, findDigest(release, asset.Name), nil
	}
	sort.Strings(names)
	return Asset{}, nil, fmt.Errorf("release %s has no asset for %s/%s; available: %s", release.Tag, goos, goarch, strings.Join(names, ", "))
}

// GeoAssets 返回 geo 数据的两个文件及其摘要。
func GeoAssets(release Release) (map[string]Asset, map[string]*Asset, error) {
	assets, digests := map[string]Asset{}, map[string]*Asset{}
	for _, asset := range release.Assets {
		if asset.Name == "geoip.dat" || asset.Name == "geosite.dat" {
			assets[asset.Name] = asset
			digests[asset.Name] = findDigest(release, asset.Name)
		}
	}
	if len(assets) != 2 {
		return nil, nil, fmt.Errorf("release %s does not contain geoip.dat and geosite.dat", release.Tag)
	}
	return assets, digests, nil
}

func findDigest(release Release, name string) *Asset {
	for _, suffix := range []string{".dgst", ".sha256sum", ".sha256"} {
		for _, asset := range release.Assets {
			if asset.Name == name+suffix {
				copy := asset
				return &copy
			}
		}
	}
	return nil
}

var hexPattern = regexp.MustCompile(`(?i)\b[0-9a-f]{64}\b`)

// ParseDigest 从摘要文件中取出 sha256：支持 xray 的 .dgst（SHA2-256= 行）和 sha256sum 格式。
func ParseDigest(text string) (string, error) {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "SHA2-256") || strings.HasPrefix(strings.TrimSpace(line), "SHA256") {
			if match := hexPattern.FindString(line); match != "" {
				return strings.ToLower(match), nil
			}
		}
	}
	if strings.Contains(text, "SHA") && strings.Contains(text, "=") {
		return "", fmt.Errorf("digest file has no SHA2-256 entry")
	}
	if match := hexPattern.FindString(text); match != "" {
		return strings.ToLower(match), nil
	}
	return "", fmt.Errorf("digest file has no sha256 value")
}

// SHA256 返回内容的十六进制 sha256。
func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Verify 校验内容的 sha256。
func Verify(data []byte, expected string) error {
	if actual := SHA256(data); !strings.EqualFold(actual, strings.TrimSpace(expected)) {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", actual, expected)
	}
	return nil
}

// Extract 从下载内容中取出组件文件：xray、mihomo 返回单个二进制，geo 返回 geoip.dat/geosite.dat，
// 自身返回管理服务二进制。拒绝链接文件、路径穿越和成员不明确的归档。member 用于手工指定归档成员。
func Extract(component, name string, data []byte, member string) (map[string][]byte, error) {
	lower := strings.ToLower(name)
	var files map[string][]byte
	var err error
	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		var reader *gzip.Reader
		if reader, err = gzip.NewReader(bytes.NewReader(data)); err == nil {
			defer reader.Close()
			files, err = readTar(reader, member)
		}
	case strings.HasSuffix(lower, ".tar"):
		files, err = readTar(bytes.NewReader(data), member)
	case strings.HasSuffix(lower, ".zip"):
		files, err = readZip(data, member)
	case strings.HasSuffix(lower, ".gz"):
		var reader *gzip.Reader
		if reader, err = gzip.NewReader(bytes.NewReader(data)); err == nil {
			defer reader.Close()
			var content []byte
			if content, err = io.ReadAll(reader); err == nil {
				files = map[string][]byte{strings.TrimSuffix(filepath.Base(name), ".gz"): content}
			}
		}
	default:
		files = map[string][]byte{filepath.Base(name): data}
	}
	if err != nil {
		return nil, err
	}
	return selectFiles(component, files, member)
}

func selectFiles(component string, files map[string][]byte, member string) (map[string][]byte, error) {
	if component == Geo {
		selected := map[string][]byte{}
		for name, content := range files {
			base := filepath.Base(name)
			if base == "geoip.dat" || base == "geosite.dat" {
				if _, exists := selected[base]; exists {
					return nil, fmt.Errorf("archive contains duplicate %s", base)
				}
				selected[base] = content
			}
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("geo package must contain geoip.dat or geosite.dat")
		}
		return selected, nil
	}
	if member != "" {
		var matches []string
		for name := range files {
			if name == member || filepath.Base(name) == filepath.Base(member) {
				matches = append(matches, name)
			}
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("archive member %s matched %d files", member, len(matches))
		}
		return map[string][]byte{component: files[matches[0]]}, nil
	}
	if len(files) == 1 {
		for _, content := range files {
			return map[string][]byte{component: content}, nil
		}
	}
	var matches []string
	for name := range files {
		if filepath.Base(name) == component {
			matches = append(matches, name)
		}
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("archive has %d files; specify the archive member for %s", len(files), component)
	}
	return map[string][]byte{component: files[matches[0]]}, nil
}

func readZip(data []byte, member string) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if file.FileInfo().Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("archive links are not supported: %s", file.Name)
		}
		if err := safePath(file.Name); err != nil {
			return nil, err
		}
		handle, err := file.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(handle)
		_ = handle.Close()
		if err != nil {
			return nil, err
		}
		files[file.Name] = content
	}
	return files, nil
}

func readTar(reader io.Reader, member string) (map[string][]byte, error) {
	tarReader := tar.NewReader(reader)
	files := map[string][]byte{}
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		if header.FileInfo().IsDir() {
			continue
		}
		if header.Typeflag == tar.TypeSymlink || header.Typeflag == tar.TypeLink {
			return nil, fmt.Errorf("archive links are not supported: %s", header.Name)
		}
		if err := safePath(header.Name); err != nil {
			return nil, err
		}
		content, err := io.ReadAll(tarReader)
		if err != nil {
			return nil, err
		}
		files[header.Name] = content
	}
}

func safePath(name string) error {
	clean := filepath.Clean(name)
	if filepath.IsAbs(name) || strings.Contains(name, "\\") || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return fmt.Errorf("archive member path is unsafe: %s", name)
	}
	return nil
}
