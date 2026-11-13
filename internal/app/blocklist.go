package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"proxystack-web/internal/access"
	"proxystack-web/internal/configdoc"
)

// errBlocklistUnchanged 表示名单已是目标状态，不写配置修订。
var errBlocklistUnchanged = errors.New("blocklist unchanged")

// BlockDomainResult 是一键拉黑或移出名单后的状态。
type BlockDomainResult struct {
	// Domain 是归并后的主域名，Entry 是名单中的条目（拦截该域名及全部子域名）。
	Domain string `json:"domain"`
	Entry  string `json:"entry"`
	// Changed 为假表示名单原本就是目标状态。
	Changed bool `json:"changed"`
	// Listed 是操作后该主域名在哪个名单中被整体拦截：global、user 或空。
	Listed string `json:"listed" enum:",global,user"`
}

// SetDomainBlocked 把主域名加入或移出全局（userID 为 0）或某个用户的域名黑名单。条目拦截该主域名及全部子域名；
// 移出只删除与它相同的条目，由上级域名条目拦截时报错，需在名单中修改。改动和其他配置修改一样等待应用。
func (s *Service) SetDomainBlocked(ctx context.Context, actor Actor, name string, userID int64, blocked bool) (*BlockDomainResult, error) {
	// 条目总是主域名本身（拦截它及全部子域名）：带前缀或无法写入名单的输入直接拒绝，不留到整份配置校验时才报错。
	entry := access.Domain(name)
	if normalized, ok := configdoc.NormalizeBlockedDomain(entry); entry == "" || strings.Contains(name, ":") || !ok || normalized != entry {
		return nil, &FieldError{Field: "domain", Message: "must be a domain name"}
	}
	result := &BlockDomainResult{Domain: entry, Entry: entry}
	verb := "加入"
	if !blocked {
		verb = "移出"
	}
	change := &ConfigChange{Action: ActionConfigSettings, Object: "settings", Scope: "settings"}
	doc, err := s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
		list := &doc.Settings.Traffic.BlockedDomains
		change.Summary = fmt.Sprintf("%s全局域名黑名单：%s", verb, entry)
		if userID != 0 {
			index := doc.UserByID(userID)
			if index < 0 {
				return ErrNotFound
			}
			user := &doc.Users[index]
			list = &user.BlockedDomains
			change.Action, change.Object, change.Scope = ActionConfigUser, "user:"+user.Name, fmt.Sprintf("users[%d]", index)
			change.Summary = fmt.Sprintf("%s用户 %s 的域名黑名单：%s", verb, user.Name, entry)
		}
		present := slices.Contains(*list, entry)
		switch {
		case blocked && present, !blocked && !present && !coveredBy(*list, entry):
			return errBlocklistUnchanged
		case blocked:
			*list = append(*list, entry)
		case present:
			*list = slices.DeleteFunc(*list, func(item string) bool { return item == entry })
		default:
			return &FieldError{Field: "domain", Message: "该域名由名单中的上级域名条目拦截，请在名单中修改"}
		}
		return nil
	})
	switch {
	case errors.Is(err, errBlocklistUnchanged):
		doc, err = s.LoadConfig(ctx)
		if err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		result.Changed = true
	}
	var own []string
	if userID != 0 {
		if index := doc.UserByID(userID); index >= 0 {
			own = doc.Users[index].BlockedDomains
		}
	}
	result.Listed = blockListed(entry, doc.Settings.Traffic.BlockedDomains, own)
	return result, nil
}

// coveredBy 判断主域名是否被名单中的某个（非 full:）条目整体拦截。
func coveredBy(entries []string, name string) bool {
	return blockListed(name, entries, nil) == BlockListedGlobal
}
