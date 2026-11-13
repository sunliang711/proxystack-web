package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/store/dbq"
)

const (
	// healthAlertInterval 是后台健康巡检的间隔。
	healthAlertInterval = time.Minute
	// healthAlertStreak 是确认异常、加重或恢复所需的连续巡检次数，避免检查项在临界值附近反复跳动时连续发信。
	healthAlertStreak = 2
)

// 连续计数的方向：同一检查项的方向改变时重新计数，异常与正常交替出现不会累计成「连续两次」。
const (
	towardAlert    = "alert"
	towardEscalate = "escalate"
	towardRecover  = "recover"
)

// healthStreak 是检查项朝同一方向变化的连续次数。
type healthStreak struct {
	toward string
	count  int
}

// healthAlertState 记录各检查项与已告警状态不一致的连续次数。已告警的项保存在数据库中，重启后继续生效。
type healthAlertState struct {
	mu     sync.Mutex
	streak map[string]healthStreak
}

// alertChecks 返回参与告警的检查项。只执行不依赖外网、开销小的检查；unit 状态按每处不一致拆分。
func (s *Service) alertChecks(ctx context.Context) []HealthCheck {
	page, alerts := s.unitChecks(ctx)
	out := append(page, alerts...)
	out = append(out, s.checkCollector(ctx)...)
	return append(out, s.checkStorage(ctx)...)
}

func healthSeverity(status string) int {
	switch status {
	case HealthFail:
		return 2
	case HealthWarn:
		return 1
	}
	return 0
}

// healthChange 是一轮巡检中状态发生变化的检查项。
type healthChange struct {
	key   string
	check HealthCheck
	since time.Time
	// escalated 表示已告警的项由警告加重为失败。
	escalated bool
	// before 是确认异常时的检查结果，恢复时与当前结果一起列出。
	before string
}

// splitHealthKey 把告警标识拆回分组和标题。
func splitHealthKey(key string) (group, title string) {
	parts := strings.SplitN(key, "/", 3)
	if len(parts) < 2 {
		return key, ""
	}
	return parts[0], parts[1]
}

// CheckHealthAlerts 执行一轮巡检，按连续两轮的结果切换状态：
//   - 未告警的项连续异常时记录告警并发送异常通知；
//   - 已告警的项由警告加重为失败时再次发送异常通知；
//   - 已告警的项连续正常，或连续两轮不再出现（问题消失或所属 Stack 已删除）时发送恢复通知。
//
// 查询失败的分组本轮结果未知，其中已告警的项保持原状；未通过但不属于告警范围的结论（例如 xray 未运行）同样保持原状。
func (s *Service) CheckHealthAlerts(ctx context.Context) {
	s.healthAlert.mu.Lock()
	defer s.healthAlert.mu.Unlock()
	if s.healthAlert.streak == nil {
		s.healthAlert.streak = map[string]healthStreak{}
	}
	alarmed, err := s.Store.Q.ListHealthAlerts(ctx)
	if err != nil {
		slog.Warn("health alerts", "err", err)
		return
	}
	alarmedByKey := map[string]dbq.HealthAlert{}
	for _, row := range alarmed {
		alarmedByKey[row.CheckKey] = row
	}
	checks := s.alertChecks(ctx)
	now := s.Now()
	bad := map[string]HealthCheck{}
	current := map[string]HealthCheck{}
	frozen := []string{}
	for _, check := range checks {
		current[check.key()] = check
		if check.alert && check.Status != HealthOK {
			bad[check.key()] = check
		}
		if check.freeze != "" {
			frozen = append(frozen, check.freeze)
		}
	}
	isFrozen := func(key string) bool {
		return slices.ContainsFunc(frozen, func(prefix string) bool { return strings.HasPrefix(key, prefix) })
	}
	// 与已告警状态一致的项清零计数，不一致的项累加，达到次数后切换状态。
	var raised, recovered []healthChange
	ongoing := []HealthCheck{}
	seen := map[string]bool{}
	bump := func(key, toward string) bool {
		streak := s.healthAlert.streak[key]
		if streak.toward != toward {
			streak = healthStreak{toward: toward}
		}
		streak.count++
		s.healthAlert.streak[key] = streak
		return streak.count >= healthAlertStreak
	}
	for key, check := range bad {
		seen[key] = true
		row, ok := alarmedByKey[key]
		switch {
		case !ok:
			if bump(key, towardAlert) {
				raised = append(raised, healthChange{key: key, check: check, since: now})
			}
		case healthSeverity(check.Status) > healthSeverity(row.Status):
			if bump(key, towardEscalate) {
				raised = append(raised, healthChange{key: key, check: check, since: time.Unix(row.Since, 0), escalated: true})
			} else {
				ongoing = append(ongoing, check)
			}
		default:
			delete(s.healthAlert.streak, key)
			ongoing = append(ongoing, check)
		}
	}
	for key, row := range alarmedByKey {
		if seen[key] {
			continue
		}
		seen[key] = true
		check, present := current[key]
		group, title := splitHealthKey(key)
		stale := HealthCheck{Group: group, Title: title, Status: row.Status, Detail: row.Detail}
		switch {
		case present && check.Status != HealthOK:
			delete(s.healthAlert.streak, key)
			ongoing = append(ongoing, check)
			continue
		case isFrozen(key):
			delete(s.healthAlert.streak, key)
			stale.Detail += "（本轮无法检查）"
			ongoing = append(ongoing, stale)
			continue
		}
		if !bump(key, towardRecover) {
			ongoing = append(ongoing, stale)
			continue
		}
		if !present {
			check = HealthCheck{Group: group, Title: title, Status: HealthOK, Detail: "该问题已消失或所属 stack 已删除"}
		}
		recovered = append(recovered, healthChange{key: key, check: check, since: time.Unix(row.Since, 0), before: row.Detail})
	}
	for key := range s.healthAlert.streak {
		if !seen[key] {
			delete(s.healthAlert.streak, key)
		}
	}
	if len(raised) == 0 && len(recovered) == 0 {
		return
	}
	sortChanges(raised)
	sortChanges(recovered)
	sortChecks(ongoing)
	settings, err := s.loadSettings(ctx)
	if err != nil {
		slog.Warn("health alerts", "err", err)
		return
	}
	mail := healthMail{loc: location(settings), now: now}
	if info, err := s.Info(ctx); err == nil {
		mail.link = info.PanelURL + "health"
	}
	if used, err := s.dbUsedBytes(ctx); err == nil {
		mail.database = s.dbSizeDetail(used)
	}
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		for _, change := range raised {
			if err := q.UpsertHealthAlert(ctx, dbq.UpsertHealthAlertParams{
				CheckKey: change.key, Since: change.since.Unix(), Status: change.check.Status, Detail: change.check.Detail,
			}); err != nil {
				return err
			}
		}
		for _, change := range recovered {
			if err := q.DeleteHealthAlert(ctx, change.key); err != nil {
				return err
			}
		}
		if len(raised) > 0 {
			if err := mail.insert(ctx, q, settings, configdoc.EventHealthAlert, raised, ongoing); err != nil {
				return err
			}
		}
		if len(recovered) > 0 {
			stillBad := slices.Clone(ongoing)
			for _, change := range raised {
				stillBad = append(stillBad, change.check)
			}
			sortChecks(stillBad)
			if err := mail.insert(ctx, q, settings, configdoc.EventHealthRecovered, recovered, stillBad); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("health alerts", "err", err)
		return
	}
	for _, change := range append(raised, recovered...) {
		delete(s.healthAlert.streak, change.key)
	}
}

func sortChanges(changes []healthChange) {
	slices.SortFunc(changes, func(a, b healthChange) int { return strings.Compare(a.key, b.key) })
}

func sortChecks(checks []HealthCheck) {
	slices.SortFunc(checks, func(a, b HealthCheck) int { return strings.Compare(a.key(), b.key()) })
}

func healthLine(check HealthCheck) string {
	line := fmt.Sprintf("- %s / %s：%s", check.Group, check.Title, check.Detail)
	if check.Hint != "" {
		line += "（处理建议：" + check.Hint + "）"
	}
	return line
}

// healthMail 是一轮巡检中各封健康通知共用的信息。
type healthMail struct {
	loc      *time.Location
	now      time.Time
	link     string
	database string
}

// insert 记录一封健康通知：changes 是本次异常、加重或恢复的项，ongoing 是其余仍未恢复的项。
func (m healthMail) insert(ctx context.Context, q *dbq.Queries, settings configdoc.Settings, event string,
	changes []healthChange, ongoing []HealthCheck) error {
	label := eventLabels[event]
	titles, keys := []string{}, make([]string, 0, len(changes))
	for _, change := range changes {
		if !slices.Contains(titles, change.check.Title) {
			titles = append(titles, change.check.Title)
		}
		keys = append(keys, change.key)
	}
	lines := []string{"事件：" + label}
	if event == configdoc.EventHealthAlert {
		lines = append(lines, "", "新出现或加重的问题：")
		for _, change := range changes {
			line := healthLine(change.check)
			if change.escalated {
				line += "（由警告加重为失败，异常开始于 " + change.since.In(m.loc).Format(time.DateTime) + "）"
			}
			lines = append(lines, line)
		}
	} else {
		lines = append(lines, "", "已恢复：")
		for _, change := range changes {
			lines = append(lines, fmt.Sprintf("- %s / %s：%s（异常开始于 %s；当前：%s）", change.check.Group, change.check.Title,
				change.before, change.since.In(m.loc).Format(time.DateTime), change.check.Detail))
		}
	}
	if len(ongoing) > 0 {
		lines = append(lines, "", "仍未恢复的问题：")
		for _, check := range ongoing {
			lines = append(lines, healthLine(check))
		}
	}
	lines = append(lines, "", "检测时间："+m.now.In(m.loc).Format(time.DateTime))
	// 磁盘空间不足时，数据库大小有助于判断是否由流量明细占用。
	if m.database != "" {
		lines = append(lines, "数据库："+m.database)
	}
	if m.link != "" {
		lines = append(lines, "健康检查："+m.link)
	}
	status, reason := notifyStatus(settings.Notify, event)
	rows, err := q.InsertNotification(ctx, dbq.InsertNotificationParams{
		At: m.now.Unix(), Event: event, DedupKey: fmt.Sprintf("%s:%d:%s:%s", event, m.now.UnixNano(), rand.Text(), strings.Join(keys, "|")),
		Subject: fmt.Sprintf("[proxystack-web] %s：%s", label, strings.Join(titles, "、")),
		Body:    strings.Join(lines, "\n") + "\n", Status: status, Error: reason,
	})
	if err == nil && rows == 0 {
		// 记录未写入时回滚告警状态，下一轮重新判定，避免状态已切换却没有通知。
		err = errors.New("duplicate health notification")
	}
	return err
}

func (s *Service) runHealthAlerts(ctx context.Context) {
	ticker := time.NewTicker(healthAlertInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.CheckHealthAlerts(ctx)
		}
	}
}
