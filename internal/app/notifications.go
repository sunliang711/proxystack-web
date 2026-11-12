package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/notify"
	"proxystack-web/internal/store/dbq"
)

// 通知状态：pending 等待发送，sent 已发送，failed 重试后仍失败，skipped 未启用邮件或未订阅该事件，只在界面显示。
const (
	NotifyPending = "pending"
	NotifySent    = "sent"
	NotifyFailed  = "failed"
	NotifySkipped = "skipped"
)

// notifyPollInterval 是发送队列的检查间隔。
const notifyPollInterval = 15 * time.Second

// notifyRetryDelays 是发送失败后的重试间隔，用完后记为失败。
var notifyRetryDelays = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}

var eventLabels = map[string]string{
	configdoc.EventDailyLimit:      "达到每日用量阈值",
	configdoc.EventDailyWarning:    "每日用量预警",
	configdoc.EventQuotaLimit:      "超出月度额度",
	configdoc.EventQuotaWarning:    "额度预警",
	configdoc.EventExpired:         "用户已到期",
	configdoc.EventExpiring:        "用户即将到期",
	configdoc.EventEgressDown:      "Stack 出口异常",
	configdoc.EventEgressRecovered: "Stack 出口恢复",
}

type notifyUser struct {
	id     int64
	name   string
	remark string
}

// pendingEvent 是判定时发现的通知事件。period 限定去重范围：同一用户或 Stack 的同一事件在一个周期内只记录一次。
type pendingEvent struct {
	event     string
	user      notifyUser
	stackID   int64
	stackName string
	period    string
	lines     []string
}

func formatBytes(value int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	amount := float64(value)
	index := 0
	for amount >= 1024 && index < len(units)-1 {
		amount /= 1024
		index++
	}
	if index == 0 {
		return fmt.Sprintf("%d B", value)
	}
	return fmt.Sprintf("%.2f %s", amount, units[index])
}

func actionLine(paused bool) string {
	if paused {
		return "执行的动作：已暂停该用户"
	}
	return "执行的动作：仅通知"
}

func usageEvent(event string, user notifyUser, period, label string, used, limit int64, paused bool) pendingEvent {
	return pendingEvent{event: event, user: user, period: period, lines: []string{
		fmt.Sprintf("%s：%s / %s", label, formatBytes(used), formatBytes(limit)), actionLine(paused),
	}}
}

func expiryEvent(event string, user notifyUser, period string, expire time.Time, paused bool) pendingEvent {
	return pendingEvent{event: event, user: user, period: period, lines: []string{
		"到期时间：" + expire.Format(time.DateTime), actionLine(paused),
	}}
}

func stackUsageEvent(stackID int64, stackName, period string, used, limit int64) pendingEvent {
	return pendingEvent{event: configdoc.EventDailyLimit, stackID: stackID, stackName: stackName, period: period, lines: []string{
		"每日用量：" + formatBytes(used) + " / " + formatBytes(limit), "执行的动作：已停止该 Stack",
	}}
}

// notifyStatus 决定新记录的通知是否进入发送队列；不发送时返回写在记录上的具体原因。
func notifyStatus(settings configdoc.NotifySettings, event string) (status, reason string) {
	switch {
	case !settings.Enabled:
		return NotifySkipped, "邮件通知未开启，只记录不发送"
	case !slices.Contains(settings.Events, event):
		return NotifySkipped, fmt.Sprintf("通知事件中未勾选「%s」，只记录不发送", eventLabels[event])
	}
	return NotifyPending, ""
}

// enqueueNotification 记录一条通知。邮件未启用或未订阅该事件时只记录、不发送。
func (s *Service) enqueueNotification(ctx context.Context, settings configdoc.Settings, event pendingEvent) {
	key := fmt.Sprintf("%s:%d:%s", event.event, event.user.id, event.period)
	if event.stackID != 0 {
		key = fmt.Sprintf("%s:stack:%d:%s", event.event, event.stackID, event.period)
	}
	if s.autoPause.notified[key] {
		return
	}
	status, reason := notifyStatus(settings.Notify, event.event)
	label := eventLabels[event.event]
	subjectName := event.user.name
	lines := []string{"事件：" + label}
	if event.stackID != 0 {
		subjectName = event.stackName
		lines = append(lines, "Stack："+event.stackName)
	} else {
		lines = append(lines, "用户："+event.user.name)
		if event.user.remark != "" {
			lines = append(lines, "备注："+event.user.remark)
		}
	}
	subject := fmt.Sprintf("[proxystack-web] %s：%s", label, subjectName)
	lines = append(lines, event.lines...)
	if info, err := s.Info(ctx); err == nil {
		if event.stackID != 0 {
			lines = append(lines, fmt.Sprintf("Stack 详情：%sstacks/%d", info.PanelURL, event.stackID))
		} else {
			lines = append(lines, fmt.Sprintf("用户详情：%susers/%d", info.PanelURL, event.user.id))
		}
	}
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		_, err := q.InsertNotification(ctx, dbq.InsertNotificationParams{
			At: s.Now().Unix(), Event: event.event, UserID: event.user.id, UserName: event.user.name, DedupKey: key,
			Subject: subject, Body: strings.Join(lines, "\n") + "\n", Status: status, Error: reason, StackID: event.stackID, StackName: event.stackName,
		})
		return err
	})
	if err != nil {
		slog.Warn("record notification", "event", event.event, "err", err)
		return
	}
	if s.autoPause.notified == nil {
		s.autoPause.notified = map[string]bool{}
	}
	s.autoPause.notified[key] = true
}

func smtpConfig(settings configdoc.NotifySettings) notify.Config {
	return notify.Config{
		Host: settings.Host, Port: settings.Port, Security: settings.Security, Username: settings.Username,
		Password: settings.Password, From: settings.From, To: settings.To,
	}
}

// DeliverNotifications 发送到期的通知，失败时按退避间隔重试。
func (s *Service) DeliverNotifications(ctx context.Context) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		slog.Warn("deliver notifications", "err", err)
		return
	}
	var due []dbq.Notification
	err = s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		due, err = q.ListDueNotifications(ctx, s.Now().Unix())
		return err
	})
	if err != nil {
		slog.Warn("deliver notifications", "err", err)
		return
	}
	for _, item := range due {
		s.deliverNotification(ctx, settings, item.ID)
	}
}

func (s *Service) deliverNotification(ctx context.Context, settings configdoc.Settings, id int64) {
	s.notificationMu.Lock()
	defer s.notificationMu.Unlock()
	// 队列读取后可能已经恢复、被替换或被另一发送器处理，发送前必须重新读取。
	item, err := s.Store.Q.GetNotification(ctx, id)
	if err != nil || item.Status != NotifyPending || item.NextAttemptAt > s.Now().Unix() {
		return
	}
	obsolete, err := s.obsoleteEgressNotification(ctx, item)
	if err != nil {
		slog.Warn("check egress notification", "err", err)
		return
	}
	if item.StackID != 0 && (!settings.Notify.Enabled || !slices.Contains(settings.Notify.Events, item.Event)) {
		obsolete = true
	}
	params := dbq.UpdateNotificationParams{ID: item.ID, Attempts: item.Attempts, Status: NotifySkipped}
	if obsolete {
		params.Error = "出口事件已被后续事件取代，或该事件的通知已关闭，未发送"
	} else {
		sendErr := s.sendMail(ctx, smtpConfig(settings.Notify), item.Subject, item.Body)
		params.Attempts, params.Status = item.Attempts+1, NotifySent
		if sendErr != nil {
			params.Error = sendErr.Error()
			if int(item.Attempts) < len(notifyRetryDelays) {
				params.Status = NotifyPending
				params.NextAttemptAt = s.Now().Add(notifyRetryDelays[item.Attempts]).Unix()
			} else {
				params.Status = NotifyFailed
			}
			slog.Warn("send notification", "event", item.Event, "attempt", params.Attempts, "err", sendErr)
		}
	}
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) error { return q.UpdateNotification(ctx, params) }); err != nil {
		slog.Warn("update notification", "err", err)
	}
}

func (s *Service) sendMail(ctx context.Context, config notify.Config, subject, body string, attachments ...notify.Attachment) error {
	if s.SendMail != nil {
		return s.SendMail(ctx, config, subject, body, attachments...)
	}
	return notify.Send(ctx, config, subject, body, attachments...)
}

func (s *Service) runNotifications(ctx context.Context) {
	ticker := time.NewTicker(notifyPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.DeliverNotifications(ctx)
		}
	}
}

// Notification 是通知记录页的一行。
type Notification struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	Event     string    `json:"event"`
	Label     string    `json:"label"`
	UserID    int64     `json:"user_id"`
	UserName  string    `json:"user_name"`
	StackID   int64     `json:"stack_id"`
	StackName string    `json:"stack_name"`
	Subject   string    `json:"subject"`
	Status    string    `json:"status" enum:"pending,sent,failed,skipped"`
	Error     string    `json:"error"`
	Attempts  int64     `json:"attempts"`
}

// ListNotifications 返回最近的通知记录。
func (s *Service) ListNotifications(ctx context.Context, limit int64) ([]Notification, error) {
	var rows []dbq.Notification
	err := s.Store.InTx(ctx, func(q *dbq.Queries) (err error) {
		rows, err = q.ListNotifications(ctx, limit)
		return err
	})
	out := make([]Notification, 0, len(rows))
	for _, row := range rows {
		out = append(out, Notification{
			ID: row.ID, At: time.Unix(row.At, 0), Event: row.Event, Label: eventLabels[row.Event], UserID: row.UserID,
			UserName: row.UserName, StackID: row.StackID, StackName: row.StackName, Subject: row.Subject, Status: row.Status, Error: row.Error, Attempts: row.Attempts,
		})
	}
	return out, err
}

// ResendNotification 把一条失败的记录放回发送队列。
func (s *Service) ResendNotification(ctx context.Context, id int64) error {
	s.notificationMu.Lock()
	defer s.notificationMu.Unlock()
	item, err := s.Store.Q.GetNotification(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	obsolete, err := s.obsoleteEgressNotification(ctx, item)
	if err != nil {
		return err
	}
	if obsolete {
		return &FieldError{Field: "notification", Message: "egress notification was superseded by a newer state"}
	}
	err = s.Store.InTx(ctx, func(q *dbq.Queries) error {
		if _, err := q.GetNotification(ctx, id); err != nil {
			return err
		}
		return q.UpdateNotification(ctx, dbq.UpdateNotificationParams{ID: id, Status: NotifyPending})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err == nil {
		go s.DeliverNotifications(context.WithoutCancel(ctx))
	}
	return err
}

// TestNotification 用表单当前值发送一封测试邮件，返回 SMTP 的错误。密码仍为占位时取已保存的值。
func (s *Service) TestNotification(ctx context.Context, form configdoc.NotifySettings) error {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return err
	}
	draft := settings
	draft.Notify = form
	configdoc.UnmaskSettings(&draft, &settings)
	return s.sendMail(ctx, smtpConfig(draft.Notify), "[proxystack-web] 测试邮件", "这是一封测试邮件，收到说明 SMTP 设置可用。\n")
}
