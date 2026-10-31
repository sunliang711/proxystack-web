package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/eagle/proxystack-web/internal/audit"
	"github.com/eagle/proxystack-web/internal/configdoc"
	"github.com/eagle/proxystack-web/internal/deps"
	"github.com/eagle/proxystack-web/internal/runtime"
	"github.com/eagle/proxystack-web/internal/store"
	"github.com/eagle/proxystack-web/internal/store/dbq"
	"github.com/eagle/proxystack-web/internal/units"
	"github.com/eagle/proxystack-web/internal/version"
)

// 依赖管理的审计动作。
const (
	ActionDepInstall  = "deps.install"
	ActionDepRollback = "deps.rollback"
	ActionSelfUpdate  = "system.self_update"
)

// 替换后的重启策略。
const (
	RestartSequential = "sequential"
	RestartAll        = "all"
	RestartNone       = "none"
)

// settingDepsLatest 保存最近一次检查到的各组件最新版本（运行状态，不属于配置）。
const settingDepsLatest = "deps_latest"

// DepFiles 返回组件安装后的文件路径。
func (s *Service) DepFiles(component string) map[string]string {
	switch component {
	case deps.Geo:
		return map[string]string{
			"geoip.dat":   filepath.Join(s.Layout.GeoDir(), "geoip.dat"),
			"geosite.dat": filepath.Join(s.Layout.GeoDir(), "geosite.dat"),
		}
	default:
		return map[string]string{component: runtime.BinaryPath(s.Layout, component)}
	}
}

// DepStatus 是「依赖」页一个组件卡片的内容。
type DepStatus struct {
	Component       string       `json:"component" enum:"xray,mihomo,geo"`
	Installed       bool         `json:"installed"`
	Version         string       `json:"version"`
	SHA256          string       `json:"sha256"`
	InstalledAt     time.Time    `json:"installed_at"`
	Source          string       `json:"source"`
	Platform        string       `json:"platform"`
	Previous        bool         `json:"previous" doc:"存在可回滚的上一个版本"`
	PreviousVersion string       `json:"previous_version"`
	Latest          string       `json:"latest"`
	LatestCheckedAt time.Time    `json:"latest_checked_at"`
	UpdateAvailable bool         `json:"update_available"`
	UsedBy          []string     `json:"used_by" doc:"期望运行且使用该组件的 stack"`
	History         []DepInstall `json:"history"`
}

// DepInstall 是一条安装记录。
type DepInstall struct {
	Version    string    `json:"version"`
	Platform   string    `json:"platform"`
	Source     string    `json:"source"`
	SHA256     string    `json:"sha256"`
	Actor      string    `json:"actor"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Result     string    `json:"result" enum:"succeeded,failed,rolled_back"`
	Error      string    `json:"error"`
}

type latestVersions struct {
	CheckedAt time.Time         `json:"checked_at"`
	Versions  map[string]string `json:"versions"`
}

// DepsStatus 汇总三个组件的安装状态。
func (s *Service) DepsStatus(ctx context.Context) ([]DepStatus, error) {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	var latest latestVersions
	var history map[string][]dbq.DepInstall
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if err := store.GetJSON(ctx, q, settingDepsLatest, &latest); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		history = map[string][]dbq.DepInstall{}
		for _, component := range []string{deps.Xray, deps.Mihomo, deps.Geo} {
			rows, err := q.ListDepInstalls(ctx, dbq.ListDepInstallsParams{Component: component, Limit: 20})
			if err != nil {
				return err
			}
			history[component] = rows
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := []DepStatus{}
	for _, component := range []string{deps.Xray, deps.Mihomo, deps.Geo} {
		status := DepStatus{Component: component, Platform: goruntime.GOOS + "/" + goruntime.GOARCH, UsedBy: []string{}, History: []DepInstall{}}
		if component == deps.Mihomo {
			status.Platform += " " + deps.ResolveVariant(doc.Settings.Deps.MihomoVariant, goruntime.GOARCH)
		}
		files := s.DepFiles(component)
		status.Installed = true
		var hashes []string
		for _, name := range slices.Sorted(mapKeys(files)) {
			data, err := os.ReadFile(files[name])
			if err != nil {
				status.Installed = false
				continue
			}
			hashes = append(hashes, deps.SHA256(data))
			if _, err := os.Stat(files[name] + ".prev"); err == nil {
				status.Previous = true
			}
		}
		status.SHA256 = strings.Join(hashes, " ")
		if status.Installed {
			status.Version = s.probeVersion(ctx, component, files[component])
		}
		if status.Previous && component != deps.Geo {
			status.PreviousVersion = s.probeVersion(ctx, component, files[component]+".prev")
		}
		for _, row := range history[component] {
			record := DepInstall{
				Version: row.Version, Platform: row.Platform, Source: row.Source, SHA256: row.Sha256, Actor: row.Actor,
				StartedAt: time.Unix(row.StartedAt, 0), FinishedAt: time.Unix(row.FinishedAt, 0), Result: row.Result, Error: row.Error,
			}
			status.History = append(status.History, record)
			if status.InstalledAt.IsZero() && row.Result == "succeeded" {
				status.InstalledAt, status.Source = record.FinishedAt, record.Source
				if status.Version == "" {
					status.Version = record.Version
				}
			}
		}
		status.Latest, status.LatestCheckedAt = latest.Versions[component], latest.CheckedAt
		status.UpdateAvailable = status.Installed && status.Latest != "" && component != deps.Geo &&
			strings.TrimPrefix(status.Latest, "v") != strings.TrimPrefix(status.Version, "v")
		for _, stack := range doc.Stacks {
			if !stack.Enabled || !stack.DesiredRunning {
				continue
			}
			if (component == deps.Mihomo && stack.Mihomo.Enabled) || (component != deps.Mihomo && stack.Xray.Enabled) {
				status.UsedBy = append(status.UsedBy, stack.Name)
			}
		}
		out = append(out, status)
	}
	return out, nil
}

func mapKeys(m map[string]string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for key := range m {
			if !yield(key) {
				return
			}
		}
	}
}

// probeVersion 运行二进制读取版本号；geo 数据没有版本号。
func (s *Service) probeVersion(ctx context.Context, component, path string) string {
	var args []string
	switch component {
	case deps.Xray:
		args = []string{"version"}
	case deps.Mihomo:
		args = []string{"-v"}
	default:
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if err != nil {
		return ""
	}
	fields := strings.Fields(strings.SplitN(string(output), "\n", 2)[0])
	for index, field := range fields {
		if component == deps.Xray && index == 1 {
			return "v" + strings.TrimPrefix(field, "v")
		}
		if component == deps.Mihomo && strings.HasPrefix(field, "v") {
			return field
		}
	}
	return ""
}

// fetcher 按下载代理设置创建下载器。经 stack 出站时要求该 stack 的 mihomo 正在运行。
func (s *Service) fetcher(ctx context.Context, doc *configdoc.Document, proxy configdoc.DownloadProxySettings) (*deps.HTTPFetcher, deps.ProxyConfig, error) {
	cfg := deps.ProxyConfig{Kind: deps.ProxyDirect}
	switch proxy.Mode {
	case configdoc.ProxyModeStack:
		index := doc.StackByName(proxy.Stack)
		if index < 0 {
			return nil, cfg, fmt.Errorf("download proxy stack does not exist: %s", proxy.Stack)
		}
		stack := doc.Stacks[index]
		unit := units.Name(units.Mihomo, stack.Name)
		states, err := s.Units.States(ctx, []string{unit})
		if err != nil {
			return nil, cfg, err
		}
		if !states[unit].Running() {
			return nil, cfg, fmt.Errorf("download proxy stack %s is not running; start it or choose another proxy mode", stack.Name)
		}
		listener := stack.Mihomo.Socks
		cfg.Kind = deps.ProxySOCKS5
		if proxy.Listener == "http" {
			listener, cfg.Kind = stack.Mihomo.HTTP, deps.ProxyHTTP
		}
		host := listener.Listen
		switch host {
		case "0.0.0.0":
			host = "127.0.0.1"
		case "::":
			host = "::1"
		}
		cfg.Address = net.JoinHostPort(host, strconv.Itoa(listener.Port))
		if listener.Auth == configdoc.ListenerAuthAccounts && len(listener.Accounts) > 0 {
			cfg.Username, cfg.Password = listener.Accounts[0].Username, listener.Accounts[0].Password
		}
		cfg.RemoteDNS = true
	case configdoc.ProxyModeCustom:
		cfg.Kind = deps.ProxyHTTP
		if proxy.Protocol == "socks5" {
			cfg.Kind = deps.ProxySOCKS5
		}
		cfg.Address = net.JoinHostPort(proxy.Host, strconv.Itoa(proxy.Port))
		cfg.Username, cfg.Password, cfg.RemoteDNS = proxy.Username, proxy.Password, proxy.RemoteDNS
	}
	fetcher, err := deps.NewHTTPFetcher(cfg)
	return fetcher, cfg, err
}

// ProxyTestResult 是下载代理测试结果。
type ProxyTestResult struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	Kind      string `json:"kind" doc:"失败类型：proxy_connect、proxy_auth、timeout、tls、upstream、network、config"`
	Message   string `json:"message"`
}

// TestDownloadProxy 用表单当前值（无需先保存）经代理请求 GitHub API。
func (s *Service) TestDownloadProxy(ctx context.Context, proxy configdoc.DownloadProxySettings) (*ProxyTestResult, error) {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	settings := configdoc.Settings{DownloadProxy: proxy}
	configdoc.UnmaskSettings(&settings, doc.Settings)
	fetcher, _, err := s.fetcher(ctx, doc, settings.DownloadProxy)
	if err != nil {
		return &ProxyTestResult{Kind: "config", Message: err.Error()}, nil
	}
	latency, err := fetcher.TestConnection(ctx)
	result := &ProxyTestResult{OK: err == nil, LatencyMS: latency.Milliseconds()}
	if err != nil {
		result.Message = err.Error()
		var fetchErr *deps.FetchError
		if errors.As(err, &fetchErr) {
			result.Kind = fetchErr.Kind
		}
	}
	return result, nil
}

// DepReleases 列出组件的上游版本。
func (s *Service) DepReleases(ctx context.Context, component string, includePre bool) ([]deps.Release, error) {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	fetcher, _, err := s.fetcher(ctx, doc, doc.Settings.DownloadProxy)
	if err != nil {
		return nil, &FieldError{Field: "download_proxy", Message: err.Error()}
	}
	releases, err := deps.Releases(ctx, fetcher, component, includePre)
	if err != nil {
		return nil, &FieldError{Field: "component", Message: "fetch releases: " + err.Error()}
	}
	return releases, nil
}

// CheckDepUpdates 获取各组件的最新稳定版并缓存，只检查不安装。
func (s *Service) CheckDepUpdates(ctx context.Context) error {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return err
	}
	fetcher, _, err := s.fetcher(ctx, doc, doc.Settings.DownloadProxy)
	if err != nil {
		return &FieldError{Field: "download_proxy", Message: err.Error()}
	}
	latest := latestVersions{CheckedAt: s.Now(), Versions: map[string]string{}}
	var failures []string
	for _, component := range []string{deps.Xray, deps.Mihomo, deps.Geo} {
		release, err := deps.FindRelease(ctx, fetcher, component, "latest")
		if err != nil {
			failures = append(failures, component+": "+err.Error())
			continue
		}
		latest.Versions[component] = release.Tag
	}
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) error { return store.PutJSON(ctx, q, settingDepsLatest, latest) }); err != nil {
		return err
	}
	if len(failures) > 0 {
		return &FieldError{Field: "component", Message: strings.Join(failures, "; ")}
	}
	return nil
}

// InstallRequest 是一次安装请求。Version 为空时使用依赖源设置；Upload 非空时安装上传的安装包。
type InstallRequest struct {
	Component string
	Version   string
	Restart   string
	Upload    *Upload
}

// Upload 是上传的安装包，sha256 必填。
type Upload struct {
	Filename string
	Data     []byte
	SHA256   string
}

// InstallDep 在后台作业中安装组件：下载 → 校验 → 解压 → 冒烟验证 → 替换 → 重启并检查，失败时回滚。
func (s *Service) InstallDep(ctx context.Context, actor Actor, req InstallRequest) (int64, error) {
	if !slices.Contains([]string{deps.Xray, deps.Mihomo, deps.Geo}, req.Component) {
		return 0, &FieldError{Field: "component", Message: "unknown component"}
	}
	if req.Upload != nil && !regexpSHA256(req.Upload.SHA256) {
		return 0, &FieldError{Field: "sha256", Message: "a 64-character hex sha256 is required for uploaded packages"}
	}
	title := "安装 " + req.Component
	if req.Version != "" {
		title += " " + req.Version
	}
	return s.startTask("deps", title, actor.Name, func(ctx context.Context, logf Logf) error {
		started := s.Now()
		record := dbq.InsertDepInstallParams{
			Component: req.Component, Platform: goruntime.GOOS + "/" + goruntime.GOARCH, Actor: actor.Name, StartedAt: started.Unix(),
		}
		err := s.installDep(ctx, req, &record, logf)
		record.FinishedAt, record.Result = s.Now().Unix(), "succeeded"
		if err != nil {
			record.Result, record.Error = "failed", err.Error()
			if errors.Is(err, errRolledBack) {
				record.Result = "rolled_back"
			}
		}
		_ = s.Store.InTx(ctx, func(q *dbq.Queries) error {
			if err := q.InsertDepInstall(ctx, record); err != nil {
				return err
			}
			return audit.Write(ctx, q, s.Now(), audit.Entry{
				Actor: actor.Name, SourceIP: actor.IP, Action: ActionDepInstall, Object: req.Component,
				Summary: fmt.Sprintf("%s %s：%s", req.Component, record.Version, record.Result),
			})
		})
		if err == nil {
			// 恢复后可能正等着这个依赖。
			s.resumeAfterRestore(ctx)
		}
		return err
	})
}

var errRolledBack = errors.New("rolled back to the previous version")

func regexpSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range strings.ToLower(value) {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

func (s *Service) installDep(ctx context.Context, req InstallRequest, record *dbq.InsertDepInstallParams, logf Logf) error {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return err
	}
	source := map[string]configdoc.DepSource{
		deps.Xray: doc.Settings.Deps.Xray, deps.Mihomo: doc.Settings.Deps.Mihomo, deps.Geo: doc.Settings.Deps.Geo,
	}[req.Component]
	files := map[string][]byte{}
	switch {
	case req.Upload != nil:
		logf("校验上传的安装包 %s", req.Upload.Filename)
		if err := deps.Verify(req.Upload.Data, req.Upload.SHA256); err != nil {
			return err
		}
		if files, err = deps.Extract(req.Component, req.Upload.Filename, req.Upload.Data, source.ArchiveMember); err != nil {
			return err
		}
		record.Version, record.Source, record.Sha256 = "upload", "upload:"+req.Upload.Filename, strings.ToLower(req.Upload.SHA256)
	default:
		fetcher, proxy, err := s.fetcher(ctx, doc, doc.Settings.DownloadProxy)
		if err != nil {
			return err
		}
		logf("下载代理：%s", proxy.Redacted())
		download := func(url string) ([]byte, error) {
			logf("下载 %s", url)
			return fetcher.Download(ctx, url, func(done, total int64) {
				if total > 0 {
					logf("已下载 %.1f / %.1f MiB", float64(done)/(1<<20), float64(total)/(1<<20))
				}
			})
		}
		if source.Source == "custom" && req.Version == "" {
			data, err := download(source.URL)
			if err != nil {
				return err
			}
			if err := deps.Verify(data, source.SHA256); err != nil {
				return err
			}
			logf("sha256 校验通过")
			if files, err = deps.Extract(req.Component, filepath.Base(source.URL), data, source.ArchiveMember); err != nil {
				return err
			}
			record.Version, record.Source, record.Sha256 = "custom", source.URL, deps.SHA256(data)
			break
		}
		version := req.Version
		if version == "" {
			version = source.Version
		}
		logf("解析 %s %s", req.Component, version)
		release, err := deps.FindRelease(ctx, fetcher, req.Component, version)
		if err != nil {
			return err
		}
		record.Version = release.Tag
		var assets map[string]deps.Asset
		var digests map[string]*deps.Asset
		if req.Component == deps.Geo {
			if assets, digests, err = deps.GeoAssets(release); err != nil {
				return err
			}
		} else {
			asset, digest, err := deps.SelectAsset(req.Component, release, goruntime.GOOS, goruntime.GOARCH, doc.Settings.Deps.MihomoVariant)
			if err != nil {
				return err
			}
			assets, digests = map[string]deps.Asset{asset.Name: asset}, map[string]*deps.Asset{asset.Name: digest}
		}
		var sums []string
		for _, name := range slices.Sorted(func(yield func(string) bool) {
			for key := range assets {
				if !yield(key) {
					return
				}
			}
		}) {
			asset := assets[name]
			data, err := download(asset.URL)
			if err != nil {
				return err
			}
			if digest := digests[name]; digest != nil {
				text, err := fetcher.Get(ctx, digest.URL)
				if err != nil {
					return err
				}
				expected, err := deps.ParseDigest(string(text))
				if err != nil {
					return err
				}
				if err := deps.Verify(data, expected); err != nil {
					return err
				}
				logf("%s sha256 与上游摘要一致", name)
			} else {
				logf("上游没有为 %s 提供摘要文件，跳过比对", name)
			}
			extracted, err := deps.Extract(req.Component, asset.Name, data, "")
			if err != nil {
				return err
			}
			for key, content := range extracted {
				files[key] = content
			}
			sums = append(sums, deps.SHA256(data))
			record.Source = "github:" + deps.Repo(req.Component)
		}
		record.Sha256 = strings.Join(sums, " ")
	}

	// 冒烟验证：先在暂存目录确认新二进制能运行，并接受当前全部生成配置。
	staging := filepath.Join(s.Layout.DownloadsDir(), "staging", req.Component)
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o750); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	for name, content := range files {
		mode := os.FileMode(0o640)
		if req.Component != deps.Geo {
			mode = 0o750
		}
		if err := os.WriteFile(filepath.Join(staging, name), content, mode); err != nil {
			return err
		}
	}
	if req.Component != deps.Geo {
		staged := filepath.Join(staging, req.Component)
		probed := s.probeVersion(ctx, req.Component, staged)
		if probed == "" {
			return fmt.Errorf("the new %s binary does not run on this machine", req.Component)
		}
		logf("新版本 %s 可以运行", probed)
		if record.Version == "upload" || record.Version == "custom" {
			record.Version = probed
		}
		checker := runtime.BinaryChecker{Layout: s.Layout, Binaries: map[string]string{req.Component: staged}}
		stacks, err := s.disk().Stacks()
		if err != nil {
			return err
		}
		var rejected []string
		for _, stack := range stacks {
			current, err := s.disk().Read(stack)
			if err != nil {
				return err
			}
			content := current.Get(req.Component)
			if content == nil {
				continue
			}
			if err := checker.Check(ctx, req.Component, stack, content); err != nil {
				rejected = append(rejected, err.Error())
			}
		}
		if len(rejected) > 0 {
			return fmt.Errorf("the new version rejects the current configs, nothing was replaced: %s", strings.Join(rejected, "; "))
		}
		logf("新版本接受全部 %d 个 stack 的配置", len(stacks))
	}

	// 替换：旧文件保留为 .prev，只保留一个上一版本。
	targets := s.DepFiles(req.Component)
	replaced := []string{}
	for name := range files {
		target, ok := targets[name]
		if !ok {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		if _, err := os.Stat(target); err == nil {
			if err := os.Rename(target, target+".prev"); err != nil {
				return err
			}
		}
		if err := os.Rename(filepath.Join(staging, name), target); err != nil {
			return err
		}
		replaced = append(replaced, target)
	}
	logf("已替换 %s", strings.Join(replaced, "、"))
	if err := s.restartAfterReplace(ctx, req.Component, req.Restart, logf); err != nil {
		logf("重启检查失败，回滚到上一个版本：%v", err)
		if rollbackErr := s.swapPrevious(req.Component); rollbackErr != nil {
			return fmt.Errorf("%v; rollback failed: %v", err, rollbackErr)
		}
		_ = s.restartAfterReplace(ctx, req.Component, RestartAll, logf)
		return fmt.Errorf("%w: %v", errRolledBack, err)
	}
	logf("安装完成")
	return nil
}

// RollbackDep 切换回上一个版本并按策略重启。
func (s *Service) RollbackDep(ctx context.Context, actor Actor, component, restart string) (int64, error) {
	return s.startTask("deps", "回滚 "+component, actor.Name, func(ctx context.Context, logf Logf) error {
		if err := s.swapPrevious(component); err != nil {
			return err
		}
		logf("已切换回上一个版本")
		err := s.restartAfterReplace(ctx, component, restart, logf)
		_ = s.Store.InTx(ctx, func(q *dbq.Queries) error {
			return audit.Write(ctx, q, s.Now(), audit.Entry{Actor: actor.Name, SourceIP: actor.IP, Action: ActionDepRollback, Object: component, Summary: "回滚 " + component})
		})
		return err
	})
}

// swapPrevious 交换当前文件和 .prev，交换后可以再次回滚回来。
func (s *Service) swapPrevious(component string) error {
	for _, target := range s.DepFiles(component) {
		previous := target + ".prev"
		if _, err := os.Stat(previous); err != nil {
			if component == deps.Geo {
				continue
			}
			return fmt.Errorf("no previous version of %s", component)
		}
		temp := target + ".swap"
		if err := os.Rename(target, temp); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(previous, target); err != nil {
			return err
		}
		if _, err := os.Stat(temp); err == nil {
			if err := os.Rename(temp, previous); err != nil {
				return err
			}
		}
	}
	return nil
}

// restartAfterReplace 重启使用该组件且正在运行的 unit，并检查它们保持运行、监听端口已打开。
func (s *Service) restartAfterReplace(ctx context.Context, component, strategy string, logf Logf) error {
	unitComponent := units.Xray
	if component == deps.Mihomo {
		unitComponent = units.Mihomo
	}
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return err
	}
	stacks, err := s.disk().Stacks()
	if err != nil {
		return err
	}
	names := []string{}
	for _, stack := range stacks {
		files, err := s.disk().Read(stack)
		if err != nil {
			return err
		}
		content := files.Get(unitComponent)
		if content == nil || (component == deps.Geo && !bytes.Contains(content, []byte("geoip:")) && !bytes.Contains(content, []byte("geosite:"))) {
			continue
		}
		names = append(names, units.Name(unitComponent, stack))
	}
	states, err := s.Units.States(ctx, names)
	if err != nil {
		return err
	}
	running := []string{}
	for _, unit := range names {
		if states[unit].Running() {
			running = append(running, unit)
		}
	}
	if len(running) == 0 {
		logf("没有正在运行且受影响的服务")
		return nil
	}
	if strategy == RestartNone {
		logf("暂不重启：%s 仍在运行旧版本，下次应用或重启时生效", strings.Join(running, "、"))
		return nil
	}
	check := func(batch []string) error {
		select {
		case <-time.After(s.healthWait()):
		case <-ctx.Done():
			return ctx.Err()
		}
		after, err := s.Units.States(ctx, batch)
		if err != nil {
			return err
		}
		for _, unit := range batch {
			if !after[unit].Running() {
				return fmt.Errorf("%s did not stay running (%s)", unit, after[unit].Active)
			}
			if s.ProbePorts {
				stack, _ := splitUnit(unit)
				if err := probeStackPorts(doc, stack, unitComponent); err != nil {
					return fmt.Errorf("%s: %w", unit, err)
				}
			}
			logf("%s 运行正常", unit)
		}
		return nil
	}
	s.collectTraffic(ctx)
	if strategy == RestartAll {
		for _, unit := range running {
			logf("重启 %s", unit)
			if err := s.Units.Restart(ctx, unit); err != nil {
				return err
			}
		}
		return check(running)
	}
	for _, unit := range running {
		logf("重启 %s", unit)
		if err := s.Units.Restart(ctx, unit); err != nil {
			return err
		}
		if err := check([]string{unit}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) healthWait() time.Duration {
	if s.HealthWait > 0 {
		return s.HealthWait
	}
	return 10 * time.Second
}

// probeStackPorts 确认 stack 某个组件配置中的监听端口已经打开。
func probeStackPorts(doc *configdoc.Document, stackName, component string) error {
	index := doc.StackByName(stackName)
	if index < 0 {
		return nil
	}
	stack := doc.Stacks[index]
	type endpoint struct {
		host string
		port int
	}
	var endpoints []endpoint
	if component == units.Xray {
		for _, inbound := range stack.Xray.Inbounds {
			endpoints = append(endpoints, endpoint{inbound.Listen, inbound.Port})
		}
	} else {
		endpoints = append(endpoints, endpoint{stack.Mihomo.Socks.Listen, stack.Mihomo.Socks.Port}, endpoint{"127.0.0.1", stack.Mihomo.ControllerPort})
		if stack.Mihomo.HTTP.Enabled {
			endpoints = append(endpoints, endpoint{stack.Mihomo.HTTP.Listen, stack.Mihomo.HTTP.Port})
		}
	}
	for _, e := range endpoints {
		host := e.host
		switch host {
		case "0.0.0.0", "":
			host = "127.0.0.1"
		case "::":
			host = "::1"
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(e.port)), 2*time.Second)
		if err != nil {
			return fmt.Errorf("port %d is not listening", e.port)
		}
		_ = conn.Close()
	}
	return nil
}

// SelfVersion 是管理服务的当前版本和最新版本。
type SelfVersion struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	Notes           string `json:"notes"`
}

// CheckSelfUpdate 查询管理服务的最新发布版本。
func (s *Service) CheckSelfUpdate(ctx context.Context) (*SelfVersion, error) {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	fetcher, _, err := s.fetcher(ctx, doc, doc.Settings.DownloadProxy)
	if err != nil {
		return nil, &FieldError{Field: "download_proxy", Message: err.Error()}
	}
	release, err := deps.FindRelease(ctx, fetcher, deps.Self, "latest")
	if err != nil {
		return nil, &FieldError{Field: "version", Message: err.Error()}
	}
	return &SelfVersion{
		Current: version.Version, Latest: release.Tag, Notes: release.Notes,
		UpdateAvailable: strings.TrimPrefix(release.Tag, "v") != strings.TrimPrefix(version.Version, "v"),
	}, nil
}

// SelfUpdate 下载并校验新版本，原子替换当前二进制，然后请求以特定退出码退出，由 systemd 重启。
// 代理进程由独立的 unit 承载，不受影响。
func (s *Service) SelfUpdate(ctx context.Context, actor Actor) (int64, error) {
	return s.startTask("self-update", "更新 proxystack-web", actor.Name, func(ctx context.Context, logf Logf) error {
		doc, err := s.LoadConfig(ctx)
		if err != nil {
			return err
		}
		fetcher, proxy, err := s.fetcher(ctx, doc, doc.Settings.DownloadProxy)
		if err != nil {
			return err
		}
		logf("下载代理：%s", proxy.Redacted())
		release, err := deps.FindRelease(ctx, fetcher, deps.Self, "latest")
		if err != nil {
			return err
		}
		asset, digest, err := deps.SelectAsset(deps.Self, release, goruntime.GOOS, goruntime.GOARCH, "")
		if err != nil {
			return err
		}
		if digest == nil {
			return fmt.Errorf("release %s has no sha256 file for %s", release.Tag, asset.Name)
		}
		logf("下载 %s %s", release.Tag, asset.Name)
		data, err := fetcher.Download(ctx, asset.URL, nil)
		if err != nil {
			return err
		}
		text, err := fetcher.Get(ctx, digest.URL)
		if err != nil {
			return err
		}
		expected, err := deps.ParseDigest(string(text))
		if err != nil {
			return err
		}
		if err := deps.Verify(data, expected); err != nil {
			return err
		}
		logf("sha256 校验通过")
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		if executable, err = filepath.EvalSymlinks(executable); err != nil {
			return err
		}
		staged := executable + ".new"
		if err := os.WriteFile(staged, data, 0o750); err != nil {
			return err
		}
		if out, err := exec.CommandContext(ctx, staged, "--version").CombinedOutput(); err != nil {
			_ = os.Remove(staged)
			return fmt.Errorf("the new binary does not run: %s", strings.TrimSpace(string(out)))
		}
		if err := os.Rename(executable, executable+".prev"); err != nil {
			return err
		}
		if err := os.Rename(staged, executable); err != nil {
			_ = os.Rename(executable+".prev", executable)
			return err
		}
		_ = s.Store.InTx(ctx, func(q *dbq.Queries) error {
			return audit.Write(ctx, q, s.Now(), audit.Entry{Actor: actor.Name, SourceIP: actor.IP, Action: ActionSelfUpdate, Object: "proxystack-web", Summary: version.Version + " → " + release.Tag})
		})
		logf("已替换为 %s，管理服务即将重启", release.Tag)
		if s.RequestRestart != nil {
			go func() {
				time.Sleep(time.Second)
				s.RequestRestart()
			}()
		}
		return nil
	})
}
