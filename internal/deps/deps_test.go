package deps_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/eagle/proxystack-web/internal/deps"
)

func TestSelectAssetPicksPlatformAndVariant(t *testing.T) {
	release := deps.Release{Tag: "v1.19.27", Assets: []deps.Asset{
		{Name: "mihomo-linux-amd64-v1.19.27.gz"},
		{Name: "mihomo-linux-amd64-compatible-v1.19.27.gz"},
		{Name: "mihomo-linux-amd64-go120-v1.19.27.gz"},
		{Name: "mihomo-linux-arm64-v1.19.27.gz"},
		{Name: "Xray-linux-64.zip"},
		{Name: "Xray-linux-64.zip.dgst"},
	}}
	asset, _, err := deps.SelectAsset(deps.Mihomo, release, "linux", "amd64", deps.VariantCompatible)
	require.NoError(t, err)
	require.Equal(t, "mihomo-linux-amd64-compatible-v1.19.27.gz", asset.Name)
	asset, _, err = deps.SelectAsset(deps.Mihomo, release, "linux", "amd64", deps.VariantStandard)
	require.NoError(t, err)
	require.Equal(t, "mihomo-linux-amd64-v1.19.27.gz", asset.Name)
	asset, digest, err := deps.SelectAsset(deps.Xray, release, "linux", "amd64", "")
	require.NoError(t, err)
	require.Equal(t, "Xray-linux-64.zip", asset.Name)
	require.Equal(t, "Xray-linux-64.zip.dgst", digest.Name)
	_, _, err = deps.SelectAsset(deps.Xray, release, "linux", "mips", "")
	require.Error(t, err)
}

func TestParseDigest(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	got, err := deps.ParseDigest("MD5= 0123\nSHA1= 4567\nSHA2-256= " + hash + "\nSHA2-512= ff\n")
	require.NoError(t, err)
	require.Equal(t, hash, got)
	got, err = deps.ParseDigest(hash + "  geoip.dat\n")
	require.NoError(t, err)
	require.Equal(t, hash, got)
}

func zipOf(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := writer.Create(name)
		require.NoError(t, err)
		_, _ = w.Write([]byte(content))
	}
	require.NoError(t, writer.Close())
	return buf.Bytes()
}

func TestExtractSelectsBinaryAndRejectsTraversal(t *testing.T) {
	files, err := deps.Extract(deps.Xray, "Xray-linux-64.zip", zipOf(t, map[string]string{"xray": "bin", "geoip.dat": "g", "LICENSE": "l"}), "")
	require.NoError(t, err)
	require.Equal(t, map[string][]byte{"xray": []byte("bin")}, files)

	_, err = deps.Extract(deps.Xray, "bad.zip", zipOf(t, map[string]string{"../xray": "bin"}), "")
	require.ErrorContains(t, err, "unsafe")

	files, err = deps.Extract(deps.Geo, "geoip.dat", []byte("data"), "")
	require.NoError(t, err)
	require.Contains(t, files, "geoip.dat")
}

// TestHTTPProxySendsCredentials 验证经 HTTP 代理下载时带上 Basic 认证，认证失败归类为代理认证错误。
func TestHTTPProxySendsCredentials(t *testing.T) {
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:secret"))
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != want {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		_, _ = w.Write([]byte("payload for " + r.URL.String()))
	}))
	defer proxyServer.Close()
	address := strings.TrimPrefix(proxyServer.URL, "http://")

	fetcher, err := deps.NewHTTPFetcher(deps.ProxyConfig{Kind: deps.ProxyHTTP, Address: address, Username: "user", Password: "secret"})
	require.NoError(t, err)
	data, err := fetcher.Get(context.Background(), "http://example.invalid/file")
	require.NoError(t, err)
	require.Equal(t, "payload for http://example.invalid/file", string(data))

	fetcher, err = deps.NewHTTPFetcher(deps.ProxyConfig{Kind: deps.ProxyHTTP, Address: address, Username: "user", Password: "wrong"})
	require.NoError(t, err)
	_, err = fetcher.Get(context.Background(), "http://example.invalid/file")
	var fetchErr *deps.FetchError
	require.True(t, errors.As(err, &fetchErr))
	require.Equal(t, deps.ErrProxyAuth, fetchErr.Kind)
}

// TestSelectSelfAssetIgnoresPackages 验证自更新在发布产物里选裸二进制和它的摘要，不会选到 tar.gz。
func TestSelectSelfAssetIgnoresPackages(t *testing.T) {
	names := []string{
		"proxystack-web-linux-amd64", "proxystack-web-linux-amd64.sha256",
		"proxystack-web-linux-arm64", "proxystack-web-linux-arm64.sha256",
		"proxystack-web-v1.2.3-linux-arm64.tar.gz", "proxystack-web-v1.2.3-linux-arm64.tar.gz.sha256",
	}
	release := deps.Release{Tag: "v1.2.3"}
	for _, name := range names {
		release.Assets = append(release.Assets, deps.Asset{Name: name, URL: "https://example.com/" + name})
	}
	asset, digest, err := deps.SelectAsset(deps.Self, release, "linux", "arm64", "")
	require.NoError(t, err)
	require.Equal(t, "proxystack-web-linux-arm64", asset.Name)
	require.NotNil(t, digest)
	require.Equal(t, "proxystack-web-linux-arm64.sha256", digest.Name)
}
