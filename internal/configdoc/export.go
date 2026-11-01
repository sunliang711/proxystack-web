package configdoc

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

// 导出范围。
const (
	ScopeAll         = "all"
	ScopeStacks      = "stacks"
	ScopeUsers       = "users"
	ScopeManualNodes = "manual_nodes"
	ScopeTemplates   = "templates"
)

// ExportScope 描述要导出的实体。
type ExportScope struct {
	Scope                  string
	Stacks                 []string
	Users                  []string
	IncludeReferencedUsers bool
	// ManualNodes 是要导出的手工节点 ID，Scope 为 manual_nodes 时使用。
	ManualNodes []int64
}

// Subset 按范围取出文档的一部分并清空 ID，用于导出文件。
func Subset(doc *Document, scope ExportScope, exportedAt time.Time) (*Document, error) {
	out := &Document{
		Schema:     Schema,
		Version:    Version,
		ExportedAt: exportedAt.UTC().Format(time.RFC3339),
		Scope:      scope.Scope,
		Secrets:    SecretsPlain,
	}
	source := doc.Clone()
	switch scope.Scope {
	case ScopeAll:
		out.Settings = source.Settings
		out.Users = source.Users
		out.Stacks = source.Stacks
		out.ManualNodes = source.ManualNodes
		out.RemoteSources = source.RemoteSources
		out.Templates = source.Templates
	case ScopeStacks:
		users := []string{}
		for _, name := range scope.Stacks {
			index := source.StackByName(name)
			if index < 0 {
				return nil, fmt.Errorf("stack does not exist: %s", name)
			}
			stack := source.Stacks[index]
			out.Stacks = append(out.Stacks, stack)
			for _, user := range stack.DirectUsers {
				if !slices.Contains(users, user) {
					users = append(users, user)
				}
			}
			for _, inbound := range stack.Xray.Inbounds {
				for _, ref := range inbound.Users {
					if !slices.Contains(users, ref.User) {
						users = append(users, ref.User)
					}
				}
			}
		}
		if scope.IncludeReferencedUsers {
			for _, user := range source.Users {
				if slices.Contains(users, user.Name) {
					out.Users = append(out.Users, user)
				}
			}
		}
	case ScopeUsers:
		for _, name := range scope.Users {
			index := source.UserByName(name)
			if index < 0 {
				return nil, fmt.Errorf("user does not exist: %s", name)
			}
			out.Users = append(out.Users, source.Users[index])
		}
		for _, node := range source.ManualNodes {
			if slices.Contains(scope.Users, node.User) {
				out.ManualNodes = append(out.ManualNodes, node)
			}
		}
	case ScopeManualNodes:
		for _, node := range source.ManualNodes {
			if slices.Contains(scope.ManualNodes, node.ID) {
				out.ManualNodes = append(out.ManualNodes, node)
			}
		}
	case ScopeTemplates:
		out.Templates = source.Templates
	default:
		return nil, fmt.Errorf("unknown scope: %s", scope.Scope)
	}
	for index := range out.Users {
		clearUserIDs(&out.Users[index])
	}
	for index := range out.Stacks {
		clearStackIDs(&out.Stacks[index])
	}
	for index := range out.ManualNodes {
		out.ManualNodes[index].ID = 0
	}
	for index := range out.RemoteSources {
		out.RemoteSources[index].ID = 0
	}
	return out, nil
}

// CSVColumns 是用户 CSV 的列。
var CSVColumns = []string{"name", "remark", "quota_gb", "reset_mode", "reset_day", "expire_at", "uuid", "ss_password", "note"}

const bytesPerGB = 1 << 30

// UsersToCSV 把用户导出为 CSV。
func UsersToCSV(users []User) ([]byte, error) {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	if err := writer.Write(CSVColumns); err != nil {
		return nil, err
	}
	for _, user := range users {
		quota := strconv.FormatFloat(float64(user.QuotaBytes)/bytesPerGB, 'f', -1, 64)
		if err := writer.Write([]string{
			user.Name, user.Remark, quota, user.ResetMode, strconv.Itoa(user.ResetDay),
			user.ExpireAt, user.UUID, user.SSPassword, user.Note,
		}); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	return buf.Bytes(), writer.Error()
}

// UsersFromCSV 解析用户 CSV，首行为表头；uuid、ss_password 留空的由导入时生成。
// 行号错误以 csv[<行>].<列> 作为路径返回。
func UsersFromCSV(data []byte) (*Document, Issues) {
	var issues Issues
	reader := csv.NewReader(bytes.NewReader(data))
	reader.TrimLeadingSpace = true
	header, err := reader.Read()
	if err != nil {
		issues.add("csv", "cannot read header: %s", err.Error())
		return nil, issues
	}
	columns := map[string]int{}
	for index, name := range header {
		columns[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "\ufeff")))] = index
	}
	if _, ok := columns["name"]; !ok {
		issues.add("csv", "the name column is required")
		return nil, issues
	}
	doc := &Document{Schema: Schema, Version: Version, Scope: ScopeUsers, Secrets: SecretsOmitted}
	for line := 2; ; line++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		path := fmt.Sprintf("csv[%d]", line)
		if err != nil {
			issues.add(path, "%s", err.Error())
			continue
		}
		get := func(column string) string {
			if index, ok := columns[column]; ok && index < len(record) {
				return strings.TrimSpace(record[index])
			}
			return ""
		}
		user := User{
			Name: get("name"), Enabled: true, Remark: get("remark"), ResetMode: get("reset_mode"),
			ExpireAt: get("expire_at"), UUID: get("uuid"), SSPassword: get("ss_password"), Note: get("note"),
			Profiles: []Profile{},
		}
		if user.ResetMode == "" {
			user.ResetMode = "none"
		}
		if value := get("quota_gb"); value != "" {
			quota, err := strconv.ParseFloat(value, 64)
			if err != nil || quota < 0 {
				issues.add(path+".quota_gb", "must be a non-negative number")
			}
			user.QuotaBytes = int64(quota * bytesPerGB)
		}
		if value := get("reset_day"); value != "" {
			day, err := strconv.Atoi(value)
			if err != nil {
				issues.add(path+".reset_day", "must be an integer")
			}
			user.ResetDay = day
		}
		if user.ExpireAt != "" {
			if parsed, err := parseFlexibleTime(user.ExpireAt); err == nil {
				user.ExpireAt = parsed.Format(time.RFC3339)
			} else {
				issues.add(path+".expire_at", "must be a date (YYYY-MM-DD) or RFC 3339 time")
			}
		}
		doc.Users = append(doc.Users, user)
	}
	if len(doc.Users) == 0 && len(issues) == 0 {
		issues.add("csv", "the file contains no users")
	}
	return doc, issues
}

func parseFlexibleTime(value string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	return time.ParseInLocation("2006-01-02", value, time.Local)
}
