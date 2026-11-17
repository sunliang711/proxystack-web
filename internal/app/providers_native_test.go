package app_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/generator/mihomo"
	"proxystack-web/internal/mihomoapi"
	"proxystack-web/internal/runtime"
	"proxystack-web/internal/units"
)

// TestNativeProviderDownloads checks real core downloads and cache retention using local-only endpoints.
// Set PSW_TEST_MIHOMO to an installed core binary; ordinary unit tests never download dependencies.
func TestNativeProviderDownloads(t *testing.T) {
	binary := os.Getenv("PSW_TEST_MIHOMO")
	if binary == "" {
		t.Skip("set PSW_TEST_MIHOMO to run native provider integration tests")
	}
	for _, mode := range []string{"direct", "http", "socks5"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int64
			var payload atomic.Value
			payload.Store("proxies:\n  - {name: HK-good, type: socks5, server: 127.0.0.1, port: 9}\n  - {name: HK-expired, type: socks5, server: 127.0.0.1, port: 9}\n  - {name: JP, type: socks5, server: 127.0.0.1, port: 9}\n")
			var proxyAuthenticated atomic.Bool
			serve := func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodConnect {
					proxyAuthenticated.Store(r.Header.Get("Proxy-Authorization") == "Basic "+base64.StdEncoding.EncodeToString([]byte("user:password")))
					conn, rw, err := w.(http.Hijacker).Hijack()
					if err != nil {
						return
					}
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
					_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
					if err := rw.Flush(); err != nil {
						return
					}
					request, err := http.ReadRequest(rw.Reader)
					if err != nil {
						return
					}
					_ = request.Body.Close()
					requests.Add(1)
					body := payload.Load().(string)
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
					return
				}
				requests.Add(1)
				if mode == "http" {
					proxyAuthenticated.Store(r.Header.Get("Proxy-Authorization") == "Basic "+base64.StdEncoding.EncodeToString([]byte("user:password")))
				}
				_, _ = io.WriteString(w, payload.Load().(string))
			}
			endpoint := httptest.NewServer(http.HandlerFunc(serve))
			defer endpoint.Close()
			proxyAddress := strings.TrimPrefix(endpoint.URL, "http://")
			if mode == "socks5" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				defer listener.Close()
				proxyAddress = listener.Addr().String()
				go func() {
					for {
						conn, err := listener.Accept()
						if err != nil {
							return
						}
						go serveProviderSOCKS(conn, &requests, &payload, &proxyAuthenticated)
					}
				}()
			}
			h := newRuntimeHarness(t)
			doc, err := h.svc.LoadConfig(context.Background())
			require.NoError(t, err)
			source := configdoc.Provider{ID: 1, Name: "airport", URL: endpoint.URL + "/subscription?token=test", FetchMode: "direct", Interval: 60, Filter: "(?i)hk", ExcludeFilter: "expired"}
			if mode != "direct" {
				source.FetchMode = "system"
				source.URL = "http://subscription.invalid/subscription?token=test"
				host, port, err := net.SplitHostPort(proxyAddress)
				require.NoError(t, err)
				proxyPort, err := strconv.Atoi(port)
				require.NoError(t, err)
				doc.Settings.DownloadProxy = configdoc.DownloadProxySettings{Mode: "custom", Protocol: mode, Host: host, Port: proxyPort, Username: "user", Password: "password", RemoteDNS: true}
			}
			stack := &doc.Stacks[1]
			stack.Mihomo.Providers = []configdoc.Provider{source}
			stack.Mihomo.Groups[0].Proxies = nil
			stack.Mihomo.Groups[0].Use = []string{source.Name}
			require.Empty(t, configdoc.Validate(doc))
			set, err := configdoc.Compile(doc)
			require.NoError(t, err)
			text, err := mihomo.DumpsConfig(set, stack.Name)
			require.NoError(t, err)
			var native map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(text), &native))
			control, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			address := control.Addr().String()
			require.NoError(t, control.Close())
			native["external-controller"] = address
			// Isolate provider behavior from geodata downloads and existing fixture listener ports.
			native["listeners"] = []any{}
			native["rules"] = []string{"MATCH," + stack.Mihomo.Groups[0].Name}
			content, err := yaml.Marshal(native)
			require.NoError(t, err)
			dir := t.TempDir()
			file := filepath.Join(dir, "config.yaml")
			require.NoError(t, os.WriteFile(file, content, 0o600))
			ctx, cancel := context.WithCancel(context.Background())
			cmd := exec.CommandContext(ctx, binary, "-d", dir, "-f", file)
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			require.NoError(t, cmd.Start())
			defer func() {
				cancel()
				_ = cmd.Wait()
				if t.Failed() {
					t.Log(output.String())
				}
			}()
			client := mihomoapi.Client{Base: "http://" + address, Secret: stack.Mihomo.ControllerSecret, HTTP: &http.Client{Timeout: 2 * time.Second}}
			var current map[string]mihomoapi.Provider
			require.Eventually(t, func() bool {
				current, err = client.Providers(context.Background())
				return err == nil && len(current[source.RuntimeName()].Proxies) == 1
			}, 8*time.Second, 20*time.Millisecond)
			require.Contains(t, current[source.RuntimeName()].Proxies[0].Name, "HK-good")
			if mode != "direct" {
				require.True(t, proxyAuthenticated.Load(), "proxy authentication was used")
			}
			initialRequests := requests.Load()
			time.Sleep(120 * time.Millisecond)
			require.Equal(t, initialRequests, requests.Load(), "interval=0 disables native periodic downloads")
			payload.Store("proxies: [broken")
			require.Error(t, client.UpdateProvider(context.Background(), source.RuntimeName()))
			current, err = client.Providers(context.Background())
			require.NoError(t, err)
			require.Contains(t, current[source.RuntimeName()].Proxies[0].Name, "HK-good")
			payload.Store("proxies:\n  - {name: HK-new, type: socks5, server: 127.0.0.1, port: 9}\n")
			require.NoError(t, client.UpdateProvider(context.Background(), source.RuntimeName()))
			current, err = client.Providers(context.Background())
			require.NoError(t, err)
			require.Contains(t, current[source.RuntimeName()].Proxies[0].Name, "HK-new")
			cache, err := os.ReadFile(filepath.Join(dir, source.CachePath()))
			require.NoError(t, err)
			require.Contains(t, string(cache), "HK-new")
		})
	}
}

// serveProviderSOCKS implements only the authenticated domain-address handshake needed by this local test.
func serveProviderSOCKS(conn net.Conn, requests *atomic.Int64, payload *atomic.Value, authenticated *atomic.Bool) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(r, methods); err != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 2}); err != nil {
		return
	}
	if _, err := io.ReadFull(r, header); err != nil {
		return
	}
	username := make([]byte, int(header[1]))
	if _, err := io.ReadFull(r, username); err != nil {
		return
	}
	length, err := r.ReadByte()
	if err != nil {
		return
	}
	password := make([]byte, int(length))
	if _, err := io.ReadFull(r, password); err != nil {
		return
	}
	if _, err := conn.Write([]byte{1, 0}); err != nil {
		return
	}
	connect := make([]byte, 4)
	if _, err := io.ReadFull(r, connect); err != nil || connect[3] != 3 {
		return
	}
	length, err = r.ReadByte()
	if err != nil {
		return
	}
	host := make([]byte, int(length)+2)
	if _, err := io.ReadFull(r, host); err != nil {
		return
	}
	authenticated.Store(string(username) == "user" && string(password) == "password" && string(host[:len(host)-2]) == "subscription.invalid")
	if _, err := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80}); err != nil {
		return
	}
	request, err := http.ReadRequest(r)
	if err != nil {
		return
	}
	_ = request.Body.Close()
	requests.Add(1)
	body := payload.Load().(string)
	_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
}

// TestNativeProviderRegexValidation prevents accepting or rejecting patterns according to the wrong engine.
func TestNativeProviderRegexValidation(t *testing.T) {
	binary := os.Getenv("PSW_TEST_MIHOMO")
	if binary == "" {
		t.Skip("set PSW_TEST_MIHOMO to run native regex validation")
	}
	h := newRuntimeHarness(t)
	h.svc.Checker = runtime.BinaryChecker{Layout: h.svc.Layout, Binaries: map[string]string{units.Mihomo: binary}}
	doc, err := h.svc.LoadConfig(context.Background())
	require.NoError(t, err)
	stack := doc.Stacks[1]
	stack.Mihomo.Providers = []configdoc.Provider{{Name: "airport", URL: "https://example.com/sub", FetchMode: "direct", Interval: 3600, Filter: "a{1001}", ExcludeFilter: "^(?!expired)"}}
	saved, err := h.svc.UpdateStack(context.Background(), h.actor, stack.ID, stack)
	require.NoError(t, err, "native-valid expressions must be accepted even when Go regexp rejects them")
	invalid := saved.Clone()
	invalid.Mihomo.Providers[0].Filter = "(?P<region>HK)"
	_, err = h.svc.UpdateStack(context.Background(), h.actor, saved.ID, invalid)
	require.Error(t, err, "Go-valid syntax that the native engine rejects must not be saved")
	invalid = saved.Clone()
	invalid.Mihomo.Providers[0].ExcludeFilter = "(?="
	_, err = h.svc.UpdateStack(context.Background(), h.actor, saved.ID, invalid)
	require.Error(t, err, "exclude expressions must use the native field spelling during validation")
}
