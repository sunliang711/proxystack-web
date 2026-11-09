package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"proxystack-web/internal/configdoc"
)

// SetUserStackMember 把用户加入或移出一个 stack。
// 加入时把用户（default 档案）加进该 stack 全部进入订阅的入站，已在其中的入站保持原有档案和覆盖设置；原先不在该 stack 中时清除残留的暂停。
// 移出时从该 stack 的全部入站中去掉该用户。移出保留该 stack 上的暂停：已应用的版本里用户仍在入站中，
// 清除暂停会让用户在应用前被当场加回代理端。两者都要应用后才影响代理和订阅。
func (s *Service) SetUserStackMember(ctx context.Context, actor Actor, userID, stackID int64, member bool) (*configdoc.User, error) {
	change := &ConfigChange{Action: ActionConfigStack}
	var userIndex int
	var stackName string
	stackPosition := -1
	var inbounds []string
	doc, err := s.mutateConfig(ctx, actor, change, func(doc *configdoc.Document) error {
		userIndex = doc.UserByID(userID)
		if userIndex < 0 {
			return ErrNotFound
		}
		stackIndex := doc.StackByID(stackID)
		if stackIndex < 0 {
			return &FieldError{Field: "stack_id", Message: "stack 不存在"}
		}
		user := &doc.Users[userIndex]
		stack := &doc.Stacks[stackIndex]
		stackName, stackPosition = stack.Name, stackIndex
		for _, inbound := range stack.Xray.Inbounds {
			inbounds = append(inbounds, inbound.Name)
		}
		change.Object = "stack:" + stack.Name
		has := func(ref configdoc.UserRef) bool { return ref.User == user.Name }
		if member {
			wasMember := slices.ContainsFunc(stack.Xray.Inbounds, func(inbound configdoc.Inbound) bool { return slices.ContainsFunc(inbound.Users, has) })
			joined := 0
			for index := range stack.Xray.Inbounds {
				inbound := &stack.Xray.Inbounds[index]
				if !inbound.Sub {
					continue
				}
				joined++
				if !slices.ContainsFunc(inbound.Users, has) {
					inbound.Users = append(inbound.Users, configdoc.UserRef{User: user.Name})
				}
			}
			if joined == 0 {
				return &FieldError{Field: "stack_id", Message: fmt.Sprintf("stack %s 没有进入订阅的入站，请先在该 stack 的「入站」中添加入站并把「进入订阅」设为是", stack.Name)}
			}
			// 原先不在该 stack 中时，残留的暂停来自之前的移出，加入即表示启用；已在其中时暂停状态保持不变。
			if !wasMember {
				user.PausedStacks = slices.DeleteFunc(user.PausedStacks, func(name string) bool { return name == stack.Name })
			}
			change.Summary = fmt.Sprintf("把用户 %s 加入 stack %s", user.Name, stack.Name)
			return nil
		}
		for index := range stack.Xray.Inbounds {
			inbound := &stack.Xray.Inbounds[index]
			inbound.Users = slices.DeleteFunc(inbound.Users, has)
		}
		change.Summary = fmt.Sprintf("把用户 %s 移出 stack %s", user.Name, stack.Name)
		return nil
	})
	var validation *ValidationError
	if errors.As(err, &validation) {
		action := "加入"
		if !member {
			action = "移出"
		}
		return nil, &FieldError{Field: "stack_id", Message: fmt.Sprintf("无法%s stack %s：%s", action, stackName, describeInboundIssues(validation.Issues, stackPosition, inbounds))}
	}
	if err != nil {
		return nil, err
	}
	return &doc.Users[userIndex], nil
}

var inboundIssuePattern = regexp.MustCompile(`^stacks\[(\d+)\]\.xray\.inbounds\[(\d+)\]`)

// describeInboundIssues 把本 stack 入站的校验问题写成「入站 名称：原因」，其余问题保留原文。
func describeInboundIssues(issues configdoc.Issues, stackPosition int, inbounds []string) string {
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		if match := inboundIssuePattern.FindStringSubmatch(issue.Path); match != nil && match[1] == strconv.Itoa(stackPosition) {
			if index, _ := strconv.Atoi(match[2]); index < len(inbounds) {
				parts = append(parts, fmt.Sprintf("入站 %s：%s", inbounds[index], issue.Message))
				continue
			}
		}
		parts = append(parts, issue.Message)
	}
	return strings.Join(parts, "；")
}
