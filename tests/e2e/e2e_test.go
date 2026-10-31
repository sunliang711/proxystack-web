//go:build e2e

// Package e2e 在安装好 proxystack-web 的 Linux 机器（由 run.sh 启动的 systemd 容器）上跑完整流程：
// 首次登录改密、两步验证、依赖下载、录入用户和 stack、应用、经订阅节点代理上网、暂停用户即时生效、
// 订阅包签发与导入、流量采集、健康检查、迁移包导出、登录限速与账号锁定。
//
// 环境变量（run.sh 负责设置）：
//
//	PSW_E2E_CONTAINER  安装所在的容器名，用 docker exec 在机器内执行检查
//	PSW_E2E_URL        宿主机访问面板的地址，含随机路径，例如 http://127.0.0.1:49153/abcd
//	PSW_E2E_PASSWORD   安装脚本打印的初始管理员密码
//	PSW_E2E_PROXY      可选，下载依赖使用的代理，例如 http://user:pass@host:port 或 socks5://host:port
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"proxystack-web/internal/auth"
)

const (
	newPassword = "e2e-admin-password"
	stackName   = "edge"
	userName    = "alice"
	// clientPort 是机器内作为客户端运行的 mihomo 的混合代理端口。
	clientPort = 17890
	probeURL   = "http://www.gstatic.com/generate_204"
	subDir     = "/opt/proxystack-sub"
)

type env struct {
	t         *testing.T
	container string
	base      string
	client    *http.Client
	csrf      string
	password  string
	// totpSecret 和 lastStep 用于生成下一个未使用过的两步验证码。
	totpSecret string
	lastStep   int64
	userID     int64
	subToken   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{
		t:         t,
		container: os.Getenv("PSW_E2E_CONTAINER"),
		base:      strings.TrimRight(os.Getenv("PSW_E2E_URL"), "/"),
		password:  os.Getenv("PSW_E2E_PASSWORD"),
	}
	if e.container == "" || e.base == "" || e.password == "" {
		t.Skip("PSW_E2E_CONTAINER, PSW_E2E_URL and PSW_E2E_PASSWORD are required; run tests/e2e/run.sh")
	}
	e.resetClient()
	return e
}

func (e *env) resetClient() {
	jar, err := cookiejar.New(nil)
	require.NoError(e.t, err)
	e.client = &http.Client{Jar: jar, Timeout: 60 * time.Second}
	e.csrf = ""
}

// request 发起 JSON 请求，返回状态码、响应头和原始响应体。
func (e *env) request(method, path string, body any) (int, http.Header, []byte) {
	e.t.Helper()
	var reader io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(e.t, err)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.base+path, reader)
	require.NoError(e.t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if e.csrf != "" {
		req.Header.Set("X-CSRF-Token", e.csrf)
	}
	resp, err := e.client.Do(req)
	require.NoError(e.t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(e.t, err)
	return resp.StatusCode, resp.Header, raw
}

// call 要求状态码符合预期，把 JSON 响应解码为 map。
func (e *env) call(method, path string, body any, status int) map[string]any {
	e.t.Helper()
	code, _, raw := e.request(method, path, body)
	require.Equal(e.t, status, code, "%s %s: %s", method, path, raw)
	out := map[string]any{}
	if len(raw) > 0 && raw[0] == '{' {
		require.NoError(e.t, json.Unmarshal(raw, &out))
	}
	return out
}

// sh 在机器内以 root 执行命令，返回合并的输出。
func (e *env) sh(script string) (string, error) {
	out, err := exec.Command("docker", "exec", e.container, "bash", "-c", script).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (e *env) mustSh(script string) string {
	e.t.Helper()
	out, err := e.sh(script)
	require.NoError(e.t, err, "%s\n%s", script, out)
	return out
}

// eventually 每秒重试 check，直到成功或超时。
func (e *env) eventually(timeout time.Duration, what string, check func() (bool, string)) {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		ok, detail := check()
		if ok {
			return
		}
		last = detail
		if time.Now().After(deadline) {
			e.t.Fatalf("timed out waiting for %s: %s", what, last)
		}
		time.Sleep(time.Second)
	}
}

// waitTask 等待后台作业结束并要求成功。
func (e *env) waitTask(id any, timeout time.Duration) {
	e.t.Helper()
	path := fmt.Sprintf("/api/tasks/%v", id)
	var task map[string]any
	e.eventually(timeout, "task "+path, func() (bool, string) {
		task = e.call(http.MethodGet, path, nil, http.StatusOK)
		return task["status"] != "running", fmt.Sprint(task["status"])
	})
	require.Equal(e.t, "succeeded", task["status"], "task %v failed: %v\n%v", id, task["error"], task["log"])
}

// nextCode 返回一个时间步晚于上次使用的两步验证码，必要时等待下一个时间步。
func (e *env) nextCode() string {
	e.t.Helper()
	for auth.TOTPStep(time.Now()) <= e.lastStep {
		time.Sleep(time.Second)
	}
	e.lastStep = auth.TOTPStep(time.Now())
	code, err := auth.TOTPCode(e.totpSecret, e.lastStep)
	require.NoError(e.t, err)
	return code
}

func (e *env) login() {
	e.t.Helper()
	body := map[string]string{"username": "admin", "password": e.password}
	if e.totpSecret != "" {
		body["totp_code"] = e.nextCode()
	}
	out := e.call(http.MethodPost, "/api/auth/login", body, http.StatusOK)
	e.csrf = out["csrf_token"].(string)
}

func firstLocation(raw []byte) string {
	var problem struct {
		Errors []struct {
			Location string `json:"location"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(raw, &problem)
	if len(problem.Errors) == 0 {
		return ""
	}
	return problem.Errors[0].Location
}

func TestEndToEnd(t *testing.T) {
	e := newEnv(t)
	steps := []struct {
		name string
		run  func(*env)
	}{
		{"services are running", stepServices},
		{"first login forces a password change", stepFirstLogin},
		{"two-factor authentication", stepTOTP},
		{"download dependencies", stepDeps},
		{"create user and stack, then apply", stepConfigureAndApply},
		{"publish and import the subscription bundle", stepSubscriptionBundle},
		{"proxy through the subscription node", stepProxy},
		{"pausing a user takes effect immediately", stepPause},
		{"traffic is collected", stepTraffic},
		{"health checks pass", stepHealth},
		{"export a migration package", stepBackup},
		{"audit log records security events", stepAudit},
		{"login rate limit and account lockout", stepLoginProtection},
	}
	for _, step := range steps {
		ok := t.Run(step.name, func(t *testing.T) {
			e.t = t
			step.run(e)
		})
		if !ok {
			// 后续步骤依赖前面的状态，出错后停止。
			t.FailNow()
		}
	}
}

func stepServices(e *env) {
	out := e.mustSh("systemctl is-active proxystack-web.service proxystack-sub.service polkit.service")
	require.Equal(e.t, "active\nactive\nactive", out)
	require.Contains(e.t, e.mustSh(". /etc/os-release && echo $PRETTY_NAME"), os.Getenv("PSW_E2E_EXPECT_OS"))
}

func stepFirstLogin(e *env) {
	e.login()
	session := e.call(http.MethodGet, "/api/auth/session", nil, http.StatusOK)
	require.Equal(e.t, true, session["admin"].(map[string]any)["must_change_password"])
	e.call(http.MethodGet, "/api/system/info", nil, http.StatusForbidden)

	e.call(http.MethodPost, "/api/auth/password", map[string]string{"old_password": e.password, "new_password": newPassword}, http.StatusNoContent)
	e.password = newPassword
	info := e.call(http.MethodGet, "/api/system/info", nil, http.StatusOK)
	require.Equal(e.t, false, info["dev_mode"])
}

func stepTOTP(e *env) {
	setup := e.call(http.MethodPost, "/api/auth/totp/setup", nil, http.StatusOK)
	e.totpSecret = setup["secret"].(string)
	e.call(http.MethodPost, "/api/auth/totp/enable", map[string]string{"totp_code": e.nextCode()}, http.StatusNoContent)
	e.call(http.MethodPost, "/api/auth/logout", nil, http.StatusNoContent)

	// 密码正确但没有验证码：要求补充验证码。
	e.resetClient()
	code, _, raw := e.request(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": e.password})
	require.Equal(e.t, http.StatusUnauthorized, code)
	require.Equal(e.t, "body.totp_code", firstLocation(raw))

	e.login()
	session := e.call(http.MethodGet, "/api/auth/session", nil, http.StatusOK)
	require.Equal(e.t, true, session["admin"].(map[string]any)["totp_enabled"])

	// 同一个验证码不能再次使用。
	used, err := auth.TOTPCode(e.totpSecret, e.lastStep)
	require.NoError(e.t, err)
	other := &env{t: e.t, base: e.base}
	other.resetClient()
	code, _, raw = other.request(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": e.password, "totp_code": used})
	require.Equal(e.t, http.StatusUnauthorized, code)
	require.Equal(e.t, "body.totp_code", firstLocation(raw))
}

func stepDeps(e *env) {
	if raw := os.Getenv("PSW_E2E_PROXY"); raw != "" {
		proxy, err := url.Parse(raw)
		require.NoError(e.t, err)
		port, err := strconv.Atoi(proxy.Port())
		require.NoError(e.t, err)
		settings := e.call(http.MethodGet, "/api/config/settings", nil, http.StatusOK)
		password, _ := proxy.User.Password()
		protocol := "http"
		if strings.HasPrefix(proxy.Scheme, "socks5") {
			protocol = "socks5"
		}
		settings["download_proxy"] = map[string]any{
			"mode": "custom", "protocol": protocol, "host": proxy.Hostname(), "port": port,
			"username": proxy.User.Username(), "password": password, "remote_dns": true,
			"stack": "", "listener": "socks",
		}
		e.call(http.MethodPut, "/api/config/settings", settings, http.StatusOK)
	}
	for _, component := range []string{"xray", "mihomo", "geo"} {
		out := e.call(http.MethodPost, "/api/deps/"+component+"/install", map[string]any{"restart": "none"}, http.StatusAccepted)
		e.waitTask(out["task_id"], 10*time.Minute)
	}
	e.mustSh("/opt/proxystack-web/bin/xray version | head -1")
	e.mustSh("/opt/proxystack-web/bin/mihomo -v | head -1")
}

func stepConfigureAndApply(e *env) {
	// 订阅节点的服务器地址取「对外地址」，客户端在同一台机器上，用回环地址。
	settings := e.call(http.MethodGet, "/api/config/settings", nil, http.StatusOK)
	settings["basic"].(map[string]any)["external_host"] = "127.0.0.1"
	e.call(http.MethodPut, "/api/config/settings", settings, http.StatusOK)

	user := e.call(http.MethodPost, "/api/users", map[string]any{
		"name": userName, "enabled": true, "uuid": "", "ss_password": "", "remark": "e2e", "display_template": "", "tag": "",
		"sub_token": "", "quota_bytes": 0, "reset_mode": "none", "reset_day": 0, "expire_at": "", "note": "", "profiles": []any{},
	}, http.StatusCreated)
	e.userID = int64(user["id"].(float64))
	e.call(http.MethodPost, "/api/stacks", map[string]any{"template": "pair", "name": stackName, "labels": []string{}, "members": []string{}}, http.StatusCreated)

	pending := e.call(http.MethodGet, "/api/runtime/pending", nil, http.StatusOK)
	require.NotEmpty(e.t, pending["changes"], "creating a stack leaves changes to apply")
	out := e.call(http.MethodPost, "/api/runtime/apply", map[string]any{}, http.StatusAccepted)
	e.waitTask(out["task_id"], 3*time.Minute)

	e.eventually(30*time.Second, "stack units to run", func() (bool, string) {
		out, err := e.sh(fmt.Sprintf("systemctl is-active psw-xray@%s.service psw-mihomo@%s.service", stackName, stackName))
		return err == nil, out
	})
}

func stepSubscriptionBundle(e *env) {
	code, header, bundle := e.request(http.MethodPost, "/api/sub-bundle/publish", nil)
	require.Equal(e.t, http.StatusOK, code, string(bundle))
	require.Contains(e.t, header.Get("Content-Disposition"), "attachment")

	dir := e.t.TempDir()
	write := func(name string, data []byte) string {
		local := filepath.Join(dir, name)
		require.NoError(e.t, os.WriteFile(local, data, 0o644))
		remote := "/tmp/" + name
		out, err := exec.Command("docker", "cp", local, e.container+":"+remote).CombinedOutput()
		require.NoError(e.t, err, string(out))
		e.mustSh("chmod 0644 " + remote)
		return remote
	}
	importBundle := func(remote string) (string, error) {
		// 以 root 导入（与安装脚本给出的命令一致），导入后文件属主改回订阅端服务账号。
		return e.sh(fmt.Sprintf("proxystack-web sub --dir %s import %s", subDir, remote))
	}

	// 篡改过的包被拒收。
	tampered := append([]byte{}, bundle...)
	tampered[len(tampered)/2] ^= 0xff
	out, err := importBundle(write("tampered.pswsub", tampered))
	require.Error(e.t, err, "a tampered bundle must be rejected: %s", out)

	good := write("bundle.pswsub", bundle)
	out, err = importBundle(good)
	require.NoError(e.t, err, out)
	require.Contains(e.t, out, "1 users")

	// 同一个包再次导入视为旧包。
	out, err = importBundle(good)
	require.Error(e.t, err, "an old bundle must be rejected: %s", out)

	links := e.call(http.MethodGet, fmt.Sprintf("/api/users/%d/subscription", e.userID), nil, http.StatusOK)
	clash := links["links"].(map[string]any)["clash"].(string)
	parsed, err := url.Parse(clash)
	require.NoError(e.t, err)
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	require.GreaterOrEqual(e.t, len(parts), 3, clash)
	e.subToken = parts[len(parts)-2]
}

// subscriptionNode 从订阅端取 Clash 订阅，返回其中属于本 stack 的节点。
func (e *env) subscriptionNode() map[string]any {
	e.t.Helper()
	var body string
	e.eventually(30*time.Second, "the subscription server to serve the imported bundle", func() (bool, string) {
		out, err := e.sh(fmt.Sprintf("curl -fsS http://127.0.0.1:9444/s/%s/clash", e.subToken))
		body = out
		return err == nil, out
	})
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	require.NoError(e.t, yaml.Unmarshal([]byte(body), &doc))
	for _, proxy := range doc.Proxies {
		if proxy["type"] == "vmess" {
			return proxy
		}
	}
	e.t.Fatalf("no vmess node in the subscription:\n%s", body)
	return nil
}

// probe 经机器内的客户端访问探测地址，返回 HTTP 状态码（失败时为 000）。
func (e *env) probe() string {
	out, _ := e.sh(fmt.Sprintf("curl -s -o /dev/null -m 8 -w '%%{http_code}' -x socks5h://127.0.0.1:%d %s", clientPort, probeURL))
	return out
}

func stepProxy(e *env) {
	node := e.subscriptionNode()
	require.Equal(e.t, "127.0.0.1", node["server"])
	config := map[string]any{
		"mixed-port": clientPort,
		"mode":       "rule",
		"log-level":  "warning",
		"proxies":    []any{node},
		"rules":      []string{"MATCH," + node["name"].(string)},
	}
	raw, err := yaml.Marshal(config)
	require.NoError(e.t, err)
	e.mustSh("mkdir -p /tmp/client && cat > /tmp/client/config.yaml <<'EOF'\n" + string(raw) + "EOF")
	// 用 pid 文件结束上一次启动的客户端：按命令行匹配会误杀执行这条命令的 shell。
	e.mustSh("if [ -f /tmp/client/pid ]; then kill $(cat /tmp/client/pid) || true; fi; " +
		"nohup /opt/proxystack-web/bin/mihomo -d /tmp/client >/tmp/client/log 2>&1 & echo $! > /tmp/client/pid")
	e.eventually(60*time.Second, "traffic through the subscription node", func() (bool, string) {
		code := e.probe()
		return code == "204", code + " " + e.mustSh("tail -3 /tmp/client/log")
	})
}

func stepPause(e *env) {
	path := fmt.Sprintf("/api/users/%d/pause", e.userID)
	e.call(http.MethodPut, path, map[string]any{"enabled": false, "paused_stacks": []string{}}, http.StatusOK)
	e.eventually(15*time.Second, "the paused user to be refused", func() (bool, string) {
		code := e.probe()
		return code != "204", code
	})
	// 暂停通过 xray API 生效，不改写配置、不重启进程。
	pending := e.call(http.MethodGet, "/api/runtime/pending", nil, http.StatusOK)
	require.Empty(e.t, pending["changes"], "pausing must not leave configuration changes")

	e.call(http.MethodPut, path, map[string]any{"enabled": true, "paused_stacks": []string{}}, http.StatusOK)
	e.eventually(15*time.Second, "the resumed user to be served", func() (bool, string) {
		code := e.probe()
		return code == "204", code
	})
}

func stepTraffic(e *env) {
	for range 5 {
		e.probe()
	}
	e.eventually(90*time.Second, "traffic to be recorded", func() (bool, string) {
		overview := e.call(http.MethodGet, "/api/traffic/overview", nil, http.StatusOK)
		today := overview["today"].(map[string]any)
		total := today["up"].(float64) + today["down"].(float64)
		return total > 0, fmt.Sprint(today)
	})
}

func stepHealth(e *env) {
	code, _, raw := e.request(http.MethodGet, "/api/health-checks", nil)
	require.Equal(e.t, http.StatusOK, code)
	var checks []struct {
		Group, Title, Status, Detail string
	}
	require.NoError(e.t, json.Unmarshal(raw, &checks))
	require.NotEmpty(e.t, checks)
	for _, check := range checks {
		require.NotEqual(e.t, "fail", check.Status, "%s / %s: %s", check.Group, check.Title, check.Detail)
	}
}

func stepBackup(e *env) {
	out := e.call(http.MethodPost, "/api/backup/export", map[string]any{"passphrase": "e2e-backup-passphrase"}, http.StatusOK)
	e.waitTask(out["task_id"], 2*time.Minute)
	code, _, raw := e.request(http.MethodGet, "/api/backup/files", nil)
	require.Equal(e.t, http.StatusOK, code)
	require.Contains(e.t, string(raw), ".pswbak")
}

func stepAudit(e *env) {
	out := e.call(http.MethodGet, "/api/audit?action=admin.&limit=50", nil, http.StatusOK)
	raw, err := json.Marshal(out["items"])
	require.NoError(e.t, err)
	require.Contains(e.t, string(raw), "admin.totp_enable")
	require.Contains(e.t, string(raw), "admin.password_change")
}

// stepLoginProtection 放在最后：它会让账号进入锁定状态。
func stepLoginProtection(e *env) {
	// 成功登录清零账号和来源地址的失败计数，下面的次数从零开始算。
	e.login()
	anon := &env{t: e.t, base: e.base}
	anon.resetClient()
	for range 5 {
		code, _, _ := anon.request(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": "wrong-password"})
		require.Equal(e.t, http.StatusUnauthorized, code)
	}
	code, _, _ := anon.request(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": e.password, "totp_code": "000000"})
	require.Equal(e.t, http.StatusLocked, code, "five failures lock the account even for the right password")

	// 同一来源 15 分钟内累计 10 次失败（上面已有 6 次）后，该地址的认证请求被暂时拒绝。
	for i := range 4 {
		code, _, _ = anon.request(http.MethodPost, "/api/auth/login", map[string]string{"username": fmt.Sprintf("nobody%d", i), "password": "x"})
		require.Equal(e.t, http.StatusUnauthorized, code)
	}
	code, header, _ := anon.request(http.MethodPost, "/api/auth/login", map[string]string{"username": "nobody", "password": "x"})
	require.Equal(e.t, http.StatusTooManyRequests, code)
	require.NotEmpty(e.t, header.Get("Retry-After"))

	// 救援命令清除锁定并关闭两步验证。
	out := e.mustSh("proxystack-web admin reset-password --disable-totp")
	require.Contains(e.t, out, "New password:")
	require.Contains(e.t, out, "Two-factor authentication has been disabled.")
}
