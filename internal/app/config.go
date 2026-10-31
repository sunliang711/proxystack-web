package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"proxystack-web/internal/audit"
	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/domain"
	"proxystack-web/internal/generator/mihomo"
	"proxystack-web/internal/generator/xray"
	"proxystack-web/internal/store"
	"proxystack-web/internal/store/dbq"
)

// 配置相关的审计动作。
const (
	ActionConfigSettings   = "config.settings"
	ActionConfigUser       = "config.user"
	ActionConfigStack      = "config.stack"
	ActionConfigRollback   = "config.rollback"
	ActionConfigImport     = "config.import"
	ActionConfigExport     = "config.export"
	ActionConfigSecretView = "config.secret_view"
)

// ErrNotFound 表示请求的实体不存在。
var ErrNotFound = errors.New("not found")

// Actor 是发起配置修改的管理员及其来源。
type Actor struct {
	Name string
	IP   string
}

// ValidationError 携带整份配置的校验问题。Scope 为请求体对应的文档路径前缀（例如 stacks[2]），
// 该前缀下的问题定位到请求体字段，其余问题作为跨实体错误展示。
type ValidationError struct {
	Issues configdoc.Issues
	Scope  string
	doc    *configdoc.Document
}

func (e *ValidationError) Error() string { return e.Issues.Error() }

// FieldIssue 是翻译后的一条问题：Field 非空时是请求体内的字段路径。
type FieldIssue struct {
	Field   string
	Message string
}

// Translate 把文档路径翻译为请求体字段或可读的实体描述；定位到请求体字段的问题排在前面。
func (e *ValidationError) Translate() []FieldIssue {
	out := make([]FieldIssue, 0, len(e.Issues))
	global := make([]FieldIssue, 0)
	for _, issue := range e.Issues {
		if e.Scope != "" && (issue.Path == e.Scope || strings.HasPrefix(issue.Path, e.Scope+".")) {
			field := strings.TrimPrefix(strings.TrimPrefix(issue.Path, e.Scope), ".")
			out = append(out, FieldIssue{Field: field, Message: issue.Message})
			continue
		}
		message := issue.Message
		if issue.Path != "" {
			message = describePath(e.doc, issue.Path) + ": " + message
		}
		global = append(global, FieldIssue{Message: message})
	}
	return append(out, global...)
}

var docIndexPattern = regexp.MustCompile(`^(stacks|users)\[(\d+)\]`)

// describePath 把 stacks[1].xray... 写成 stack usa1 › xray...，便于在表单顶部阅读。
func describePath(doc *configdoc.Document, path string) string {
	match := docIndexPattern.FindStringSubmatch(path)
	if match == nil || doc == nil {
		return path
	}
	index, _ := strconv.Atoi(match[2])
	rest := strings.TrimPrefix(strings.TrimPrefix(path, match[0]), ".")
	name := match[0]
	switch match[1] {
	case "stacks":
		if index < len(doc.Stacks) {
			name = "stack " + doc.Stacks[index].Name
		}
	case "users":
		if index < len(doc.Users) {
			name = "user " + doc.Users[index].Name
		}
	}
	if rest == "" {
		return name
	}
	return name + " › " + rest
}

// ConfigChange 描述一次配置修改，用于审计和修订摘要。
type ConfigChange struct {
	Action  string
	Object  string
	Summary string
	// Scope 是校验错误定位到请求体时使用的文档路径前缀，可在 Mutate 内设置。
	Scope string
	// Notes 是保存后暂停状态当场生效时需要提示的情况。
	Notes []string
}

// LoadConfig 读取当前完整配置。
func (s *Service) LoadConfig(ctx context.Context) (*configdoc.Document, error) {
	var doc *configdoc.Document
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		var err error
		doc, err = store.LoadDocument(ctx, q)
		return err
	})
	return doc, err
}

// mutateConfig 在一个事务内读取配置、执行修改、全量校验、写回并生成修订版本和审计记录。
func (s *Service) mutateConfig(ctx context.Context, actor Actor, change *ConfigChange, fn func(doc *configdoc.Document) error) (*configdoc.Document, error) {
	var result, before *configdoc.Document
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		doc, err := store.LoadDocument(ctx, q)
		if err != nil {
			return err
		}
		before = doc.Clone()
		if err := fn(doc); err != nil {
			return err
		}
		doc.Normalize()
		if issues := configdoc.Validate(doc); len(issues) > 0 {
			return &ValidationError{Issues: issues, Scope: change.Scope, doc: doc}
		}
		if err := store.SaveDocument(ctx, q, doc); err != nil {
			return err
		}
		if err := s.writeRevision(ctx, q, actor, change.Summary, doc); err != nil {
			return err
		}
		result = doc
		return audit.Write(ctx, q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: change.Action, Object: change.Object, Summary: change.Summary,
		})
	})
	if err != nil {
		return nil, err
	}
	// 手动暂停开关不进入待应用变更：无论改动来自哪里，保存后立即生效。
	change.Notes = s.syncPauses(ctx, before, result)
	// 阈值、额度或到期时间可能改了：立刻重新判定，不等下一轮采集。
	s.evaluateAutoPause(ctx)
	return result, nil
}

func (s *Service) writeRevision(ctx context.Context, q *dbq.Queries, actor Actor, summary string, doc *configdoc.Document) error {
	snapshot, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = q.InsertConfigRevision(ctx, dbq.InsertConfigRevisionParams{
		CreatedAt: s.Now().Unix(), Actor: actor.Name, Summary: summary, Snapshot: string(snapshot),
	})
	return err
}

// reservedPorts 返回配置文档之外占用的端口（面板端口）。
func (s *Service) reservedPorts(ctx context.Context, q *dbq.Queries) []int {
	var ports []int
	if panel, err := store.GetPanelSettings(ctx, q); err == nil && panel.Port != 0 {
		ports = append(ports, panel.Port)
	}
	return ports
}

// AuditSecretView 记录一次查看明文凭据的操作。
func (s *Service) AuditSecretView(ctx context.Context, actor Actor, object string) error {
	return s.Store.InTx(ctx, func(q *dbq.Queries) error {
		return audit.Write(ctx, q, s.Now(), audit.Entry{
			Actor: actor.Name, SourceIP: actor.IP, Action: ActionConfigSecretView, Object: object, Summary: "查看明文凭据",
		})
	})
}

// UpdateSettings 保存全局设置。
func (s *Service) UpdateSettings(ctx context.Context, actor Actor, settings configdoc.Settings) (*configdoc.Document, error) {
	change := &ConfigChange{Action: ActionConfigSettings, Object: "settings", Summary: "更新系统设置", Scope: "settings"}
	return s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
		configdoc.UnmaskSettings(&settings, doc.Settings)
		doc.Settings = &settings
		return nil
	})
}

// CreateUser 新建用户。凭据为空时自动生成。
func (s *Service) CreateUser(ctx context.Context, actor Actor, user configdoc.User) (*configdoc.User, error) {
	user.ID = 0
	generated := configdoc.NewUser(user.Name)
	fillEmpty(&user.UUID, generated.UUID)
	fillEmpty(&user.SSPassword, generated.SSPassword)
	fillEmpty(&user.SubToken, generated.SubToken)
	change := &ConfigChange{Action: ActionConfigUser, Object: "user:" + user.Name, Summary: "新建用户 " + user.Name}
	doc, err := s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
		change.Scope = fmt.Sprintf("users[%d]", len(doc.Users))
		doc.Users = append(doc.Users, user)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &doc.Users[len(doc.Users)-1], nil
}

// UpdateUser 保存用户。改名、档案改名和删除档案会同步更新入站引用。
func (s *Service) UpdateUser(ctx context.Context, actor Actor, id int64, user configdoc.User) (*configdoc.User, error) {
	change := &ConfigChange{Action: ActionConfigUser}
	var index int
	doc, err := s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
		index = doc.UserByID(id)
		if index < 0 {
			return ErrNotFound
		}
		old := doc.Users[index]
		change.Object, change.Summary, change.Scope = "user:"+user.Name, "修改用户 "+user.Name, fmt.Sprintf("users[%d]", index)
		user.ID = id
		configdoc.UnmaskUser(&user, &old)
		for _, oldProfile := range old.Profiles {
			found := false
			for _, profile := range user.Profiles {
				if profile.ID != 0 && profile.ID == oldProfile.ID {
					configdoc.RenameProfile(doc, old.Name, oldProfile.Name, profile.Name)
					found = true
				}
			}
			if !found {
				configdoc.RemoveProfileRefs(doc, old.Name, oldProfile.Name)
			}
		}
		configdoc.RenameUser(doc, old.Name, user.Name)
		doc.Users[index] = user
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &doc.Users[index], nil
}

// ResetUserToken 重新生成用户的订阅 token。
func (s *Service) ResetUserToken(ctx context.Context, actor Actor, id int64) (*configdoc.User, error) {
	change := &ConfigChange{Action: ActionConfigUser}
	var index int
	doc, err := s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
		index = doc.UserByID(id)
		if index < 0 {
			return ErrNotFound
		}
		name := doc.Users[index].Name
		change.Object, change.Summary = "user:"+name, "重置用户 "+name+" 的订阅 token"
		doc.Users[index].SubToken = configdoc.RandomToken()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &doc.Users[index], nil
}

// DeleteUser 删除用户并移除入站对它的引用。
func (s *Service) DeleteUser(ctx context.Context, actor Actor, id int64) error {
	change := &ConfigChange{Action: ActionConfigUser}
	_, err := s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
		index := doc.UserByID(id)
		if index < 0 {
			return ErrNotFound
		}
		name := doc.Users[index].Name
		change.Object, change.Summary = "user:"+name, "删除用户 "+name
		configdoc.RemoveUserRefs(doc, name)
		configdoc.RemoveUserNodes(doc, name)
		doc.Users = append(doc.Users[:index], doc.Users[index+1:]...)
		return nil
	})
	return err
}

// CreateStack 按模板新建 stack。
func (s *Service) CreateStack(ctx context.Context, actor Actor, opts configdoc.NewStackOptions) (*configdoc.Stack, error) {
	change := &ConfigChange{Action: ActionConfigStack, Object: "stack:" + opts.Name, Summary: "新建 stack " + opts.Name + "（模板 " + opts.Template + "）"}
	var created *configdoc.Stack
	err := s.withReserved(ctx, func(reserved []int) error {
		opts.ReservedPorts = reserved
		doc, err := s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
			if doc.StackByName(opts.Name) >= 0 {
				return &FieldError{Field: "name", Message: "a stack with this name already exists"}
			}
			stack, err := configdoc.NewStack(doc, *doc.Settings, opts)
			if err != nil {
				return &FieldError{Field: "template", Message: err.Error()}
			}
			change.Scope = fmt.Sprintf("stacks[%d]", len(doc.Stacks))
			doc.Stacks = append(doc.Stacks, stack)
			return nil
		})
		if err == nil {
			created = &doc.Stacks[len(doc.Stacks)-1]
		}
		return err
	})
	return created, err
}

// UpdateStack 保存整个 stack。按子项 ID 识别改名，同步其他 stack 中的引用和本 stack 内的代理组、规则。
func (s *Service) UpdateStack(ctx context.Context, actor Actor, id int64, stack configdoc.Stack) (*configdoc.Stack, error) {
	change := &ConfigChange{Action: ActionConfigStack}
	var index int
	doc, err := s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
		index = doc.StackByID(id)
		if index < 0 {
			return ErrNotFound
		}
		old := doc.Stacks[index]
		stack.ID = id
		configdoc.UnmaskStack(&stack, &old)
		change.Object, change.Summary, change.Scope = "stack:"+stack.Name, "修改 stack "+stack.Name, fmt.Sprintf("stacks[%d]", index)
		for _, oldInbound := range old.Xray.Inbounds {
			for _, inbound := range stack.Xray.Inbounds {
				if inbound.ID != 0 && inbound.ID == oldInbound.ID {
					configdoc.RenameInbound(doc, old.Name, oldInbound.Name, inbound.Name)
				}
			}
		}
		configdoc.RenameStack(doc, old.Name, stack.Name)
		renameTargets(&stack, old)
		doc.Stacks[index] = stack
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &doc.Stacks[index], nil
}

// renameTargets 对 ID 不变但改了名的上游和代理组，把本 stack 内仍指向旧名字的引用改为新名字。
func renameTargets(stack *configdoc.Stack, old configdoc.Stack) {
	names := map[int64]string{}
	for _, upstream := range old.Mihomo.Upstreams {
		names[upstream.ID] = upstream.Name
	}
	for _, group := range old.Mihomo.Groups {
		names[group.ID] = group.Name
	}
	exists := map[string]bool{}
	for _, upstream := range stack.Mihomo.Upstreams {
		exists[upstream.Name] = true
	}
	for _, group := range stack.Mihomo.Groups {
		exists[group.Name] = true
	}
	rename := func(id int64, name string) {
		if oldName, ok := names[id]; ok && id != 0 && oldName != name && !exists[oldName] {
			configdoc.RenameProxyTarget(stack, oldName, name)
		}
	}
	for _, upstream := range stack.Mihomo.Upstreams {
		rename(upstream.ID, upstream.Name)
	}
	for _, group := range stack.Mihomo.Groups {
		rename(group.ID, group.Name)
	}
}

// DeleteStack 删除 stack。
func (s *Service) DeleteStack(ctx context.Context, actor Actor, id int64) error {
	change := &ConfigChange{Action: ActionConfigStack}
	_, err := s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
		index := doc.StackByID(id)
		if index < 0 {
			return ErrNotFound
		}
		name := doc.Stacks[index].Name
		change.Object, change.Summary = "stack:"+name, "删除 stack "+name
		doc.Stacks = append(doc.Stacks[:index], doc.Stacks[index+1:]...)
		configdoc.RemovePausedStack(doc, name)
		return nil
	})
	return err
}

// CloneStack 克隆 stack 并重新分配端口。
func (s *Service) CloneStack(ctx context.Context, actor Actor, id int64, name string) (*configdoc.Stack, error) {
	change := &ConfigChange{Action: ActionConfigStack, Object: "stack:" + name}
	var created *configdoc.Stack
	err := s.withReserved(ctx, func(reserved []int) error {
		doc, err := s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
			index := doc.StackByID(id)
			if index < 0 {
				return ErrNotFound
			}
			if doc.StackByName(name) >= 0 {
				return &FieldError{Field: "name", Message: "a stack with this name already exists"}
			}
			change.Summary = "克隆 stack " + doc.Stacks[index].Name + " 为 " + name
			clone := configdoc.CloneStack(&doc.Stacks[index], name)
			if err := configdoc.AllocateStackPorts(doc, *doc.Settings, &clone, reserved); err != nil {
				return &FieldError{Field: "name", Message: err.Error()}
			}
			doc.Stacks = append(doc.Stacks, clone)
			return nil
		})
		if err == nil {
			created = &doc.Stacks[len(doc.Stacks)-1]
		}
		return err
	})
	return created, err
}

// SetStackMembers 同步 auto stack 的成员。
func (s *Service) SetStackMembers(ctx context.Context, actor Actor, id int64, members []string) (*configdoc.Stack, error) {
	change := &ConfigChange{Action: ActionConfigStack}
	var index int
	doc, err := s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
		index = doc.StackByID(id)
		if index < 0 {
			return ErrNotFound
		}
		stack := &doc.Stacks[index]
		if stack.Role != "auto" {
			return &FieldError{Field: "members", Message: "only auto stacks have members"}
		}
		for _, member := range members {
			target := doc.StackByName(member)
			if target < 0 || doc.Stacks[target].InboundByName(configdoc.RelayInbound) < 0 {
				return &FieldError{Field: "members", Message: "member must be a stack with a relay inbound: " + member}
			}
		}
		change.Object, change.Summary, change.Scope = "stack:"+stack.Name, "修改 stack "+stack.Name+" 的成员", fmt.Sprintf("stacks[%d]", index)
		configdoc.SetMembers(stack, members)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &doc.Stacks[index], nil
}

func (s *Service) withReserved(ctx context.Context, fn func(reserved []int) error) error {
	var reserved []int
	if err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		reserved = s.reservedPorts(ctx, q)
		return nil
	}); err != nil {
		return err
	}
	return fn(reserved)
}

// GeneratedPreview 是一个 stack 的生成物预览。
type GeneratedPreview struct {
	Xray   string
	Mihomo string
	Errors []string
}

// PreviewStack 用当前配置生成 stack 的 xray 和 mihomo 配置。
func (s *Service) PreviewStack(ctx context.Context, id int64) (*GeneratedPreview, error) {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	index := doc.StackByID(id)
	if index < 0 {
		return nil, ErrNotFound
	}
	set, err := buildStackSet(doc)
	if err != nil {
		return &GeneratedPreview{Errors: []string{err.Error()}}, nil
	}
	name := doc.Stacks[index].Name
	preview := &GeneratedPreview{}
	stack := doc.Stacks[index]
	if stack.Enabled && stack.Xray.Enabled {
		if preview.Xray, err = xray.DumpsConfig(set, name); err != nil {
			preview.Errors = append(preview.Errors, "xray: "+err.Error())
		}
	}
	if stack.Enabled && stack.Mihomo.Enabled {
		if preview.Mihomo, err = mihomo.DumpsConfig(set, name); err != nil {
			preview.Errors = append(preview.Errors, "mihomo: "+err.Error())
		}
	}
	return preview, nil
}

// buildStackSet 把文档编译为生成器输入：展开用户引用并带上停用用户。
func buildStackSet(doc *configdoc.Document) (domain.StackSet, error) {
	set, err := configdoc.Compile(doc)
	if err != nil {
		return domain.StackSet{}, err
	}
	resolved, err := domain.ResolveStackSetUserRefs(set)
	if err != nil {
		return domain.StackSet{}, err
	}
	resolved.DisabledUsers = configdoc.DisabledUsers(doc)
	return resolved, nil
}

// PortPool 是端口池名称。
type PortPool string

// AllocatePort 从指定端口池分配一个当前配置未占用、本机也未被占用的端口。
func (s *Service) AllocatePort(ctx context.Context, pool string, exclude []int) (int, error) {
	var port int
	err := s.Store.InTx(ctx, func(q *dbq.Queries) error {
		doc, err := store.LoadDocument(ctx, q)
		if err != nil {
			return err
		}
		reserved := append(s.reservedPorts(ctx, q), exclude...)
		pools := map[string]configdoc.PortRange{
			"xray_inbound":      doc.Settings.PortPools.XrayInbound,
			"xray_api":          doc.Settings.PortPools.XrayAPI,
			"mihomo_socks":      doc.Settings.PortPools.MihomoSocks,
			"mihomo_http":       doc.Settings.PortPools.MihomoHTTP,
			"mihomo_controller": doc.Settings.PortPools.MihomoController,
		}
		rangeForPool, ok := pools[pool]
		if !ok {
			return &FieldError{Field: "pool", Message: "unknown port pool"}
		}
		for attempt := 0; attempt < 64; attempt++ {
			candidate, err := configdoc.AllocatePort(doc, rangeForPool, reserved)
			if err != nil {
				return &FieldError{Field: "pool", Message: err.Error()}
			}
			if s.PortChecker == nil || s.PortChecker.IsAvailable("0.0.0.0", candidate) {
				port = candidate
				return nil
			}
			reserved = append(reserved, candidate)
		}
		return &FieldError{Field: "pool", Message: "no free port found in the pool"}
	})
	return port, err
}

// PortCheck 是端口检查结果。
type PortCheck struct {
	ConflictWith string
	InUse        bool
}

// CheckPort 检查端口是否与其他 stack 冲突，以及（不属于本配置时）是否被本机其他进程占用。
func (s *Service) CheckPort(ctx context.Context, port int, stackID int64) (*PortCheck, error) {
	doc, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	result := &PortCheck{}
	for _, stack := range doc.Stacks {
		if stack.ID == stackID {
			continue
		}
		if configdoc.StackPorts(&stack)[port] {
			result.ConflictWith = "stack " + stack.Name
			return result, nil
		}
	}
	owned := false
	if index := doc.StackByID(stackID); index >= 0 {
		owned = configdoc.StackPorts(&doc.Stacks[index])[port]
	}
	if !owned && s.PortChecker != nil {
		result.InUse = !s.PortChecker.IsAvailable("0.0.0.0", port)
	}
	return result, nil
}

// backupDatabase 用 VACUUM INTO 在 backups 目录生成快照，返回文件路径。
func (s *Service) backupDatabase(ctx context.Context, reason string) (string, error) {
	if err := os.MkdirAll(s.Layout.BackupsDir(), 0o750); err != nil {
		return "", err
	}
	name := filepath.Join(s.Layout.BackupsDir(), fmt.Sprintf("%s-%s.db", reason, s.Now().UTC().Format("20060102-150405")))
	if _, err := s.Store.DB.ExecContext(ctx, "VACUUM INTO ?", name); err != nil {
		return "", fmt.Errorf("backup database: %w", err)
	}
	return name, nil
}

func fillEmpty(target *string, value string) {
	if *target == "" {
		*target = value
	}
}
