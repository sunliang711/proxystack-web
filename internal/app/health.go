package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/deps"
	"proxystack-web/internal/domain"
	"proxystack-web/internal/store/dbq"
	"proxystack-web/internal/units"
	"proxystack-web/internal/version"
)

// 健康检查结果。
const (
	HealthOK   = "ok"
	HealthWarn = "warn"
	HealthFail = "fail"
)

const (
	backupStaleAfter = 3 * 24 * time.Hour
	collectStale     = 2 * time.Minute
	diskWarnBytes    = 5 << 30
	diskFailBytes    = 1 << 30
)

// HealthCheck 是一个检查项。
type HealthCheck struct {
	Group  string `json:"group"`
	Title  string `json:"title"`
	Status string `json:"status" enum:"ok,warn,fail"`
	Detail string `json:"detail"`
	// Hint 是处理建议，通过时为空。
	Hint string `json:"hint"`
	// alert 表示该项未通过时由后台巡检发送告警邮件；提醒性质的项不设置。
	alert bool
	// alertID 区分同一标题下的多个告警项，例如 unit 状态中的每一处不一致。
	alertID string
	// freeze 非空表示该项本身是查询失败，告警标识以 freeze 开头的项本轮结果未知，保持原有告警状态。
	freeze string
}

// key 是检查项在告警状态中的标识。
func (c HealthCheck) key() string {
	if c.alertID != "" {
		return c.Group + "/" + c.Title + "/" + c.alertID
	}
	return c.Group + "/" + c.Title
}

const unitStateKey = "运行/unit 状态与生成物"

// HealthChecks 汇总各检查项。某一项查询失败时该项标为失败，不影响其他项。
func (s *Service) HealthChecks(ctx context.Context) []HealthCheck {
	out := []HealthCheck{{Group: "版本", Title: "管理服务", Status: HealthOK, Detail: version.Current().Version}}
	out = append(out, s.checkDeps(ctx)...)
	out = append(out, s.checkUnits(ctx)...)
	out = append(out, s.checkPending(ctx))
	out = append(out, s.checkPorts(ctx)...)
	out = append(out, s.checkHandlerService(ctx)...)
	out = append(out, s.checkCollector(ctx)...)
	out = append(out, s.checkStorage(ctx)...)
	out = append(out, s.checkNotifications(ctx))
	if status, err := s.RestoreStatusData(ctx); err == nil && status.Pending {
		out = append(out, HealthCheck{
			Group: "恢复", Title: "恢复收尾", Status: HealthWarn, Detail: "等待依赖：" + strings.Join(status.Missing, "、"),
			Hint: "在「依赖」页安装与源实例相同的版本，完成后自动重新生成配置",
		})
	}
	return out
}

func failed(group, title string, err error) HealthCheck {
	return HealthCheck{Group: group, Title: title, Status: HealthFail, Detail: err.Error()}
}

func (s *Service) checkDeps(ctx context.Context) []HealthCheck {
	status, err := s.DepsStatus(ctx)
	if err != nil {
		return []HealthCheck{failed("版本", "依赖", err)}
	}
	out := []HealthCheck{}
	for _, dep := range status {
		check := HealthCheck{Group: "版本", Title: dep.Component, Status: HealthOK, Detail: dep.Version}
		switch {
		case !dep.Installed && dep.Component == deps.Geo:
			// 缺 geo 数据时 mihomo 仍能启动，只是用到 geo 规则时要自行下载。
			check.Status, check.Detail = HealthWarn, "未安装"
			check.Hint = "在「依赖」页安装，否则 mihomo 用到 geo 规则时会自行下载"
		case !dep.Installed && len(dep.UsedBy) > 0:
			check.Status, check.Detail = HealthFail, "未安装，被 "+strings.Join(dep.UsedBy, "、")+" 使用"
			check.Hint = "在「依赖」页安装"
		case !dep.Installed:
			check.Status, check.Detail = HealthWarn, "未安装"
			check.Hint = "在「依赖」页安装"
		case dep.UpdateAvailable:
			check.Detail = fmt.Sprintf("%s，可升级到 %s", dep.Version, dep.Latest)
		}
		out = append(out, check)
	}
	return out
}

func (s *Service) checkUnits(ctx context.Context) []HealthCheck {
	page, _ := s.unitChecks(ctx)
	return page
}

// unitChecks 返回页面显示的检查项，以及按每处不一致拆分的告警项：汇总项已告警时，新出现的不一致仍能单独告警。
func (s *Service) unitChecks(ctx context.Context) (page, alerts []HealthCheck) {
	findings, err := s.Reconcile(ctx, false)
	if err != nil {
		// 查询 unit 状态失败说明管理服务无法访问 systemd，此时各处不一致的状态未知。
		check := failed("运行", "systemd 访问", err)
		check.alert, check.freeze = true, unitStateKey
		return []HealthCheck{check}, nil
	}
	const hint = "在 Stack 页启动或重启相关组件；生成物不一致时重新应用配置"
	page = []HealthCheck{{Group: "运行", Title: "systemd 访问", Status: HealthOK, Detail: "可以查询 unit 状态", alert: true}}
	state := HealthCheck{Group: "运行", Title: "unit 状态与生成物", Status: HealthOK, Detail: "与期望状态一致"}
	if len(findings) > 0 {
		state.Status, state.Detail, state.Hint = HealthFail, strings.Join(findings, "；"), hint
	}
	for _, finding := range findings {
		alerts = append(alerts, HealthCheck{Group: state.Group, Title: state.Title, Status: HealthFail, Detail: finding,
			Hint: hint, alert: true, alertID: finding})
	}
	return append(page, state), alerts
}

func (s *Service) checkPending(ctx context.Context) HealthCheck {
	check := HealthCheck{Group: "运行", Title: "待应用变更", Status: HealthOK, Detail: "没有待应用变更"}
	pending, err := s.Pending(ctx)
	switch {
	case err != nil:
		return failed("运行", "待应用变更", err)
	case pending.Error != "":
		check.Status, check.Detail = HealthFail, pending.Error
	case len(pending.Changes) > 0:
		check.Status, check.Detail = HealthWarn, fmt.Sprintf("%d 处变更尚未应用", len(pending.Changes))
		check.Hint = "点击顶栏「待应用变更」查看并应用"
	}
	return check
}

// checkPorts 检查本应运行但未运行的 stack 的端口是否被其他进程占用；运行中的 stack 占用自己的端口是正常的。
func (s *Service) checkPorts(ctx context.Context) []HealthCheck {
	check := HealthCheck{Group: "运行", Title: "端口占用", Status: HealthOK, Detail: "没有发现冲突"}
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return []HealthCheck{failed("运行", "端口占用", err)}
	}
	if s.PortChecker == nil {
		return []HealthCheck{check}
	}
	conflicts := []string{}
	for index := range doc.Stacks {
		stack := &doc.Stacks[index]
		if !stack.Enabled || !stack.DesiredRunning {
			continue
		}
		names := []string{units.Name(units.Xray, stack.Name), units.Name(units.Mihomo, stack.Name)}
		states, err := s.Units.States(ctx, names)
		if err != nil || states[names[0]].Running() || states[names[1]].Running() {
			continue
		}
		for port := range configdoc.StackPorts(stack) {
			if port != 0 && !s.PortChecker.IsAvailable("0.0.0.0", port) {
				conflicts = append(conflicts, fmt.Sprintf("%s:%d", stack.Name, port))
			}
		}
	}
	if len(conflicts) > 0 {
		check.Status, check.Detail = HealthFail, "已被其他进程占用："+strings.Join(conflicts, "、")
		check.Hint = "停止占用端口的进程，或修改 stack 的端口后应用"
	}
	return []HealthCheck{check}
}

// checkHandlerService 只提示不判失败：HandlerService 是本机无鉴权的用户管理接口，未开启时用户启停要重启 stack 才生效。
func (s *Service) checkHandlerService(ctx context.Context) []HealthCheck {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return []HealthCheck{failed("运行", "HandlerService", err)}
	}
	set, err := configdoc.Compile(doc)
	if err != nil {
		return []HealthCheck{failed("运行", "HandlerService", err)}
	}
	out := []HealthCheck{}
	for index := range set.Stacks {
		stack := &set.Stacks[index]
		if !stack.Enabled || !stack.Xray.Enabled {
			continue
		}
		api := domain.ResolveXrayAPIConfig(set.Config.Defaults.Xray, stack.Xray)
		detail := "已开启：本机可无鉴权管理用户，只监听回环地址"
		if !api.Enabled || !api.HasService("HandlerService") {
			detail = "未开启：用户启停要重启该 stack 才生效"
		}
		out = append(out, HealthCheck{Group: "运行", Title: "HandlerService：" + stack.Name, Status: HealthOK, Detail: detail})
	}
	return out
}

func (s *Service) checkCollector(ctx context.Context) []HealthCheck {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		check := failed("流量", "采集器", err)
		check.freeze = "流量/"
		return []HealthCheck{check}
	}
	if !settings.Traffic.Enabled {
		return []HealthCheck{{Group: "流量", Title: "采集器", Status: HealthWarn, Detail: "流量监控未开启，用量和阈值不生效", Hint: "在「系统设置 → 流量」中开启"}}
	}
	s.traffic.mu.Lock()
	defer s.traffic.mu.Unlock()
	out := []HealthCheck{}
	now := s.Now()
	for _, status := range s.traffic.statuses {
		check := HealthCheck{Group: "流量", Title: "采集：" + status.Stack, Status: HealthOK, Detail: "最近采集 " + status.LastAt.Format(time.DateTime)}
		// xray 未运行由 unit 状态一项告警；未开启统计接口是配置问题，只提示不告警。
		check.alert = status.Error != collectNotRunning && status.Error != collectNoStats
		switch {
		case status.Error != "":
			check.Status, check.Detail = HealthWarn, status.Error
		case now.Sub(status.LastAt) > collectStale:
			check.Status, check.Detail = HealthWarn, "超过两分钟没有采集"
		}
		out = append(out, check)
	}
	if len(out) == 0 {
		out = append(out, HealthCheck{Group: "流量", Title: "采集器", Status: HealthOK, Detail: "还没有已应用的 stack"})
	}
	return out
}

func (s *Service) checkStorage(ctx context.Context) []HealthCheck {
	out := []HealthCheck{}
	settings, err := s.loadSettings(ctx)
	var used int64
	if err == nil {
		used, err = s.dbUsedBytes(ctx)
	}
	if err != nil {
		check := failed("存储", "数据库大小", err)
		check.freeze = "存储/数据库大小"
		out = append(out, check)
	} else {
		// 按实际占用的页判断：清理释放的页留在文件中供复用，文件大小不随清理缩小。
		size := HealthCheck{Group: "存储", Title: "数据库大小", Status: HealthOK, Detail: s.dbSizeDetail(used), alert: true}
		if limit := int64(settings.Notify.DBWarnMB) << 20; used > limit {
			size.Status = HealthWarn
			size.Detail += "，超过告警阈值 " + formatBytes(limit)
			size.Hint = "缩短流量明细或访问统计的保留期，或在「系统设置 → 通知」中调高阈值"
		}
		out = append(out, size)
	}

	backup := HealthCheck{Group: "存储", Title: "最近备份", Status: HealthOK, alert: true}
	if at, ok := s.lastBackup(); ok {
		backup.Detail = at.Format(time.DateTime)
		if s.Now().Sub(at) > backupStaleAfter {
			backup.Status, backup.Hint = HealthWarn, "检查「数据库备份」定时任务"
		}
	} else {
		// 新实例在首次备份前也处于这一状态，只提示不告警。
		backup.Status, backup.Detail, backup.Hint = HealthWarn, "还没有备份", "在「定时任务」页立即执行「数据库备份」"
		backup.alert = false
	}
	out = append(out, backup)

	disk := HealthCheck{Group: "存储", Title: "磁盘剩余空间", Status: HealthOK, alert: true}
	readFree := diskFree
	if s.DiskFree != nil {
		readFree = s.DiskFree
	}
	free, err := readFree(s.Layout.DataDir())
	if err != nil {
		disk.Status, disk.Detail = HealthWarn, err.Error()
		disk.alert, disk.freeze = false, "存储/磁盘剩余空间"
	} else {
		disk.Detail = formatBytes(int64(free))
		switch {
		case free < diskFailBytes:
			disk.Status, disk.Hint = HealthFail, "清理磁盘或缩短流量明细保留期"
		case free < diskWarnBytes:
			disk.Status, disk.Hint = HealthWarn, "清理磁盘或缩短流量明细保留期"
		}
	}
	return append(out, disk)
}

// dbFileBytes 是数据库文件与 WAL 文件的总大小。
func (s *Service) dbFileBytes() int64 {
	var total int64
	for _, suffix := range []string{"", "-wal"} {
		if info, err := os.Stat(s.Layout.DBPath() + suffix); err == nil {
			total += info.Size()
		}
	}
	return total
}

// dbUsedBytes 是数据库实际占用的页大小，不含清理后空闲待复用的页。
func (s *Service) dbUsedBytes(ctx context.Context) (int64, error) {
	var used int64
	err := s.Store.DB.QueryRowContext(ctx,
		"SELECT (page_count - freelist_count) * page_size FROM pragma_page_count(), pragma_freelist_count(), pragma_page_size()").Scan(&used)
	return used, err
}

// dbSizeDetail 描述数据库大小；文件明显大于实际占用时同时给出文件大小。
func (s *Service) dbSizeDetail(used int64) string {
	detail := "已用 " + formatBytes(used)
	if file := s.dbFileBytes(); file > used+used/10+(1<<20) {
		detail += "，文件 " + formatBytes(file)
	}
	return detail
}

func (s *Service) checkNotifications(ctx context.Context) HealthCheck {
	check := HealthCheck{Group: "通知", Title: "邮件发送", Status: HealthOK, Detail: "没有失败的通知"}
	var count int64
	err := s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		count, err = q.CountFailedNotifications(ctx)
		return err
	})
	if err != nil {
		return failed("通知", "邮件发送", err)
	}
	if count > 0 {
		check.Status, check.Detail, check.Hint = HealthWarn, fmt.Sprintf("%d 封通知发送失败", count), "在「通知记录」页查看原因并重发"
	}
	return check
}

// AuditEntry 是审计日志的一行。
type AuditEntry struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	Actor    string    `json:"actor"`
	SourceIP string    `json:"source_ip"`
	Action   string    `json:"action"`
	Object   string    `json:"object"`
	Summary  string    `json:"summary"`
}

// AuditPage 是一页审计日志。
type AuditPage struct {
	Total int64        `json:"total"`
	Items []AuditEntry `json:"items"`
}

// ListAudit 按动作前缀和操作者筛选审计日志，新的在前。
func (s *Service) ListAudit(ctx context.Context, action, actor string, limit, offset int64) (*AuditPage, error) {
	if limit < 1 {
		return nil, errors.New("limit must be positive")
	}
	page := &AuditPage{Items: []AuditEntry{}}
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		var err error
		if page.Total, err = q.CountAuditLogs(ctx, dbq.CountAuditLogsParams{Action: action, Actor: actor}); err != nil {
			return err
		}
		rows, err := q.SearchAuditLogs(ctx, dbq.SearchAuditLogsParams{Action: action, Actor: actor, RowLimit: limit, RowOffset: offset})
		for _, row := range rows {
			page.Items = append(page.Items, AuditEntry{
				ID: row.ID, At: time.Unix(row.At, 0), Actor: row.Actor, SourceIP: row.SourceIp, Action: row.Action, Object: row.Object, Summary: row.Summary,
			})
		}
		return err
	})
	return page, err
}
