package configdoc

// MaskedValue 是 API 返回的凭据占位。保存时字段仍为该占位表示「保持原值」。
const MaskedValue = "••••••"

func mask(value *string) {
	if *value != "" {
		*value = MaskedValue
	}
}

func unmask(value *string, old string, found bool) {
	if *value != MaskedValue {
		return
	}
	if found {
		*value = old
		return
	}
	*value = ""
}

// MaskSettings 遮蔽设置中的凭据字段。
func MaskSettings(settings *Settings) {
	mask(&settings.DownloadProxy.Password)
	mask(&settings.NodeExport.Token)
	mask(&settings.Notify.Password)
}

// UnmaskSettings 把仍为占位的设置凭据恢复为原值。
func UnmaskSettings(settings *Settings, old *Settings) {
	unmask(&settings.DownloadProxy.Password, old.DownloadProxy.Password, true)
	unmask(&settings.NodeExport.Token, old.NodeExport.Token, true)
	unmask(&settings.Notify.Password, old.Notify.Password, true)
}

// MaskManualNode 遮蔽手工节点的凭据字段。
func MaskManualNode(node *ManualNode) {
	mask(&node.UUID)
	mask(&node.Password)
	mask(&node.AuthPassword)
}

// UnmaskManualNodes 按 ID 把仍为占位的手工节点凭据恢复为原值。
func UnmaskManualNodes(nodes []ManualNode, old []ManualNode) {
	for index := range nodes {
		node := &nodes[index]
		var previous ManualNode
		found := false
		for _, candidate := range old {
			if node.ID != 0 && candidate.ID == node.ID {
				previous, found = candidate, true
			}
		}
		unmask(&node.UUID, previous.UUID, found)
		unmask(&node.Password, previous.Password, found)
		unmask(&node.AuthPassword, previous.AuthPassword, found)
	}
}

// MaskRemoteSource 遮蔽远端节点源的 token。
func MaskRemoteSource(source *RemoteSource) {
	mask(&source.Token)
}

// UnmaskRemoteSources 按 ID 把仍为占位的 token 恢复为原值。
func UnmaskRemoteSources(sources []RemoteSource, old []RemoteSource) {
	for index := range sources {
		source := &sources[index]
		previous, found := "", false
		for _, candidate := range old {
			if source.ID != 0 && candidate.ID == source.ID {
				previous, found = candidate.Token, true
			}
		}
		unmask(&source.Token, previous, found)
	}
}

// MaskUser 遮蔽用户的凭据字段。
func MaskUser(user *User) {
	mask(&user.UUID)
	mask(&user.SSPassword)
	mask(&user.SubToken)
	for index := range user.Profiles {
		mask(&user.Profiles[index].UUID)
		mask(&user.Profiles[index].SSPassword)
	}
}

// UnmaskUser 把仍为占位的凭据恢复为 old 中的原值（档案按 ID 对应）；对应不上的清空，由校验提示必填。
func UnmaskUser(user *User, old *User) {
	unmask(&user.UUID, old.UUID, true)
	unmask(&user.SSPassword, old.SSPassword, true)
	unmask(&user.SubToken, old.SubToken, true)
	for index := range user.Profiles {
		profile := &user.Profiles[index]
		var previous Profile
		found := false
		for _, candidate := range old.Profiles {
			if profile.ID != 0 && candidate.ID == profile.ID {
				previous, found = candidate, true
			}
		}
		unmask(&profile.UUID, previous.UUID, found)
		unmask(&profile.SSPassword, previous.SSPassword, found)
	}
}

// MaskStack 遮蔽 stack 的凭据字段。
func MaskStack(stack *Stack) {
	mask(&stack.Mihomo.ControllerSecret)
	mask(&stack.Xray.Outbound.Password)
	for index := range stack.Xray.Inbounds {
		mask(&stack.Xray.Inbounds[index].Password)
		mask(&stack.Xray.Inbounds[index].AuthPassword)
	}
	for _, listener := range []*Listener{&stack.Mihomo.Socks, &stack.Mihomo.HTTP} {
		for index := range listener.Accounts {
			mask(&listener.Accounts[index].Password)
		}
	}
}

// UnmaskStack 把仍为占位的凭据恢复为 old 中的原值：入站按 ID 对应，监听账号按用户名对应。
func UnmaskStack(stack *Stack, old *Stack) {
	unmask(&stack.Mihomo.ControllerSecret, old.Mihomo.ControllerSecret, true)
	unmask(&stack.Xray.Outbound.Password, old.Xray.Outbound.Password, true)
	for index := range stack.Xray.Inbounds {
		inbound := &stack.Xray.Inbounds[index]
		var previous Inbound
		found := false
		for _, candidate := range old.Xray.Inbounds {
			if inbound.ID != 0 && candidate.ID == inbound.ID {
				previous, found = candidate, true
			}
		}
		unmask(&inbound.Password, previous.Password, found)
		unmask(&inbound.AuthPassword, previous.AuthPassword, found)
	}
	pairs := [][2]*Listener{{&stack.Mihomo.Socks, &old.Mihomo.Socks}, {&stack.Mihomo.HTTP, &old.Mihomo.HTTP}}
	for _, pair := range pairs {
		for index := range pair[0].Accounts {
			account := &pair[0].Accounts[index]
			previous, found := "", false
			for _, candidate := range pair[1].Accounts {
				if candidate.Username == account.Username {
					previous, found = candidate.Password, true
				}
			}
			unmask(&account.Password, previous, found)
		}
	}
}
