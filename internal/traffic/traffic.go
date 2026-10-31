// Package traffic 是流量统计的纯逻辑：解析 xray 计数、累加增量、计算统计周期。
package traffic

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"proxystack-web/internal/domain"
	"proxystack-web/internal/xrayapi"
)

// 统计维度。
const (
	ScopeUser     = "user"
	ScopeInbound  = "inbound"
	ScopeOutbound = "outbound"
)

// SubjectUnknown 是无法映射到当前用户的计数归属。
const SubjectUnknown = "unknown"

// apiTag 是 xray API 入站的 tag，它自身的流量不计入统计。
const apiTag = "api"

// Key 标识一条明细。
type Key struct {
	StackID int64
	Scope   string
	Subject string
}

// Bytes 是上下行字节数。
type Bytes struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// Total 返回上下行合计。
func (b Bytes) Total() int64 { return b.Up + b.Down }

// Add 累加。
func (b *Bytes) Add(other Bytes) {
	b.Up += other.Up
	b.Down += other.Down
}

// Delta 是一次采集解析出的增量。
type Delta struct {
	// Details 是各维度的明细。
	Details map[Key]Bytes
	// Users 是按用户合计的用量（档案计入所属用户），用于阈值和额度判定。
	Users map[int64]Bytes
	// Stack 是该 stack 全部入站的合计，用于实时速率。
	Stack Bytes
}

// Parse 解析一个 stack 的计数。计数名格式：user>>>EMAIL>>>traffic>>>uplink、
// inbound>>>TAG>>>traffic>>>downlink、outbound>>>TAG>>>traffic>>>uplink。
// known 判断用户 ID 是否存在；已删除用户和占位用户的计数归入 unknown。
func Parse(stackID int64, stats []xrayapi.Stat, known func(userID int64) bool) Delta {
	delta := Delta{Details: map[Key]Bytes{}, Users: map[int64]Bytes{}}
	for _, stat := range stats {
		parts := strings.Split(stat.Name, ">>>")
		if len(parts) != 4 || parts[2] != "traffic" || stat.Value <= 0 {
			continue
		}
		var bytes Bytes
		switch parts[3] {
		case "uplink":
			bytes.Up = stat.Value
		case "downlink":
			bytes.Down = stat.Value
		default:
			continue
		}
		scope, name := parts[0], parts[1]
		switch scope {
		case ScopeInbound:
			if name == apiTag {
				continue
			}
			delta.Stack.Add(bytes)
		case ScopeOutbound:
			if name == apiTag {
				continue
			}
		case ScopeUser:
			userID, profileID, ok := domain.ParseUserStatsEmail(name)
			if !ok || !known(userID) {
				name = SubjectUnknown
				break
			}
			name = Subject(userID, profileID)
			total := delta.Users[userID]
			total.Add(bytes)
			delta.Users[userID] = total
		default:
			continue
		}
		key := Key{StackID: stackID, Scope: scope, Subject: name}
		total := delta.Details[key]
		total.Add(bytes)
		delta.Details[key] = total
	}
	return delta
}

// Subject 返回用户维度明细的 subject：用户 ID，含档案时为「用户ID.档案ID」。
func Subject(userID, profileID int64) string {
	if profileID == 0 {
		return strconv.FormatInt(userID, 10)
	}
	return fmt.Sprintf("%d.%d", userID, profileID)
}

// SubjectUser 返回 subject 对应的用户 ID；unknown 等非用户 subject 返回 false。
func SubjectUser(subject string) (int64, bool) {
	head, _, _ := strings.Cut(subject, ".")
	id, err := strconv.ParseInt(head, 10, 64)
	return id, err == nil
}

// Day 返回 t 在 loc 中的日期（YYYY-MM-DD）。
func Day(t time.Time, loc *time.Location) string { return t.In(loc).Format(time.DateOnly) }

// DayStart 返回 t 所在自然日的开始时刻。
func DayStart(t time.Time, loc *time.Location) time.Time {
	local := t.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

// MonthStart 返回 t 所在自然月的开始时刻。
func MonthStart(t time.Time, loc *time.Location) time.Time {
	local := t.In(loc)
	return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
}

// PeriodStart 返回 t 所在计费周期的开始时刻（Unix 秒）。不重置的用户只有一个周期，返回 0；
// 每月重置的用户从最近一个重置日 00:00 开始。
func PeriodStart(t time.Time, loc *time.Location, resetMode string, resetDay int) int64 {
	if resetMode != "monthly" || resetDay < 1 {
		return 0
	}
	local := t.In(loc)
	start := time.Date(local.Year(), local.Month(), resetDay, 0, 0, 0, 0, loc)
	if local.Before(start) {
		start = start.AddDate(0, -1, 0)
	}
	return start.Unix()
}

// NextPeriodStart 返回下一个计费周期的开始时刻；不重置时返回零值。
func NextPeriodStart(t time.Time, loc *time.Location, resetMode string, resetDay int) time.Time {
	if resetMode != "monthly" || resetDay < 1 {
		return time.Time{}
	}
	return time.Unix(PeriodStart(t, loc, resetMode, resetDay), 0).In(loc).AddDate(0, 1, 0)
}
