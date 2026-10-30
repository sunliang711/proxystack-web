package xray

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/eagle/proxystack-web/internal/domain"
)

// SupportsUserToggle 判断某 inbound 协议是否支持按用户启停。
//
// 只有 vmess 和 shadowsocks 会渲染成多用户 clients 列表，也只有它们能被
// Xray HandlerService 的 adu/rmu 操作。socks5 和 http 是单账号 inbound，
// 把账号摘掉会退化成免认证入口，因此一律不参与启停。
func SupportsUserToggle(protocol string) bool {
	return protocol == "vmess" || protocol == "shadowsocks"
}

// enabledInboundUsers 返回过滤掉禁用用户后的 inbound 用户列表。
func enabledInboundUsers(inbound domain.Inbound, disabled domain.DisabledUserSet, stackName string) []domain.InboundUser {
	if !SupportsUserToggle(inbound.Protocol) || len(disabled) == 0 {
		return inbound.Users
	}
	users := make([]domain.InboundUser, 0, len(inbound.Users))
	for _, user := range inbound.Users {
		if disabled.Has(stackName, user.User) {
			continue
		}
		users = append(users, user)
	}
	return users
}

// PlaceholderUser 是入站全部用户都被暂停时写入的占位用户名，不会出现在任何订阅中。
const PlaceholderUser = "psw-placeholder"

// applyDisabledUsers 过滤 inbound 上被暂停的用户。
//
// 全部用户都被暂停时写入一个占位用户：空的 clients 列表会被 Xray 拒绝（vmess），
// 或让 shadowsocks 退回到 inbound 级共享密码；保留占位用户让入站继续监听，
// 之后恢复任一用户仍然可以通过 HandlerService 热添加。占位凭据由 stack 的
// controller secret 派生，生成结果稳定且无法从配置以外推算。
func applyDisabledUsers(inbound domain.Inbound, disabled domain.DisabledUserSet, stackName string, seed string) (domain.Inbound, error) {
	if !SupportsUserToggle(inbound.Protocol) || len(disabled) == 0 || len(inbound.Users) == 0 {
		return inbound, nil
	}
	users := enabledInboundUsers(inbound, disabled, stackName)
	if len(users) == len(inbound.Users) {
		return inbound, nil
	}
	if len(users) == 0 {
		users = []domain.InboundUser{placeholderUser(inbound, stackName, seed)}
	}
	inbound.Users = users
	return inbound, nil
}

func placeholderUser(inbound domain.Inbound, stackName string, seed string) domain.InboundUser {
	sum := sha256.Sum256([]byte(seed + "|" + stackName + "|" + inbound.TagOrDefault()))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	user := domain.InboundUser{
		User: PlaceholderUser,
		UUID: fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]),
	}
	switch inbound.Method {
	case "2022-blake3-aes-128-gcm":
		user.Password = base64.StdEncoding.EncodeToString(sum[16:32])
	default:
		user.Password = base64.StdEncoding.EncodeToString(sum[:32])
	}
	return user
}

// DumpsInboundUserPatch 渲染只含单个用户的 inbound 片段，供 xray api adu 使用。
//
// 复用主生成路径的 inbound 渲染，保证热添加的用户和写盘配置里的形状一致。
func DumpsInboundUserPatch(inbound domain.Inbound, user domain.InboundUser) (string, error) {
	if !SupportsUserToggle(inbound.Protocol) {
		return "", GeneratorError{Message: "inbound protocol does not support user toggle: " + inbound.Protocol}
	}
	inbound.Users = []domain.InboundUser{user}
	rendered, err := RenderInbound(inbound)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(map[string]any{"inbounds": []any{rendered}}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}
