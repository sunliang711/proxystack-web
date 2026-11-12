package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"proxystack-web/internal/configdoc"
	"proxystack-web/internal/store/dbq"
)

// 参与配置生成的设置分组 key。
const (
	SettingPortPools = "port_pools"
	SettingDefaults  = "defaults"
	SettingSecurity  = "security"
	SettingDeps      = "deps"
	SettingProxy     = "download_proxy"
	SettingSub       = "subscription"
	SettingExport    = "node_export"
	SettingTraffic   = "traffic"
	SettingNotify    = "notify"
)

// LoadSettings 读取全局设置，缺失的分组使用默认值。
func LoadSettings(ctx context.Context, q *dbq.Queries) (configdoc.Settings, error) {
	settings := configdoc.DefaultSettings()
	groups := map[string]any{
		SettingBasic:     &settings.Basic,
		SettingPortPools: &settings.PortPools,
		SettingDefaults:  &settings.Defaults,
		SettingSecurity:  &settings.Security,
		SettingDeps:      &settings.Deps,
		SettingProxy:     &settings.DownloadProxy,
		SettingSub:       &settings.Subscription,
		SettingExport:    &settings.NodeExport,
		SettingTraffic:   &settings.Traffic,
		SettingNotify:    &settings.Notify,
	}
	for key, target := range groups {
		if err := GetJSON(ctx, q, key, target); err != nil && err != ErrNotFound {
			return configdoc.Settings{}, err
		}
	}
	return settings, nil
}

// SaveSettings 写入全局设置。
func SaveSettings(ctx context.Context, q *dbq.Queries, settings configdoc.Settings) error {
	groups := map[string]any{
		SettingBasic:     settings.Basic,
		SettingPortPools: settings.PortPools,
		SettingDefaults:  settings.Defaults,
		SettingSecurity:  settings.Security,
		SettingDeps:      settings.Deps,
		SettingProxy:     settings.DownloadProxy,
		SettingSub:       settings.Subscription,
		SettingExport:    settings.NodeExport,
		SettingTraffic:   settings.Traffic,
		SettingNotify:    settings.Notify,
	}
	for key, value := range groups {
		if err := PutJSON(ctx, q, key, value); err != nil {
			return err
		}
	}
	return nil
}

// LoadDocument 从配置表组装完整文档（含设置，凭据为明文，实体带 ID）。
func LoadDocument(ctx context.Context, q *dbq.Queries) (*configdoc.Document, error) {
	settings, err := LoadSettings(ctx, q)
	if err != nil {
		return nil, err
	}
	doc := &configdoc.Document{
		Schema:   configdoc.Schema,
		Version:  configdoc.Version,
		Secrets:  configdoc.SecretsPlain,
		Settings: &settings,
		Users:    []configdoc.User{},
		Stacks:   []configdoc.Stack{},
	}

	users, err := q.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	userIndex := map[int64]int{}
	for _, row := range users {
		userIndex[row.ID] = len(doc.Users)
		doc.Users = append(doc.Users, configdoc.User{
			ID: row.ID, Name: row.Name, Enabled: row.Enabled != 0, UUID: row.Uuid, SSPassword: row.SsPassword,
			Remark: row.Remark, DisplayTemplate: row.DisplayTemplate, Tag: row.Tag, SubToken: row.SubToken,
			QuotaBytes: row.QuotaBytes, DailyLimitBytes: row.DailyLimitBytes, DailyLimitAction: row.DailyLimitAction,
			ResetMode: row.ResetMode, ResetDay: int(row.ResetDay),
			ExpireAt: row.ExpireAt, Note: row.Note, Profiles: []configdoc.Profile{},
			PauseWindowMode: row.PauseWindowMode, PauseWindows: configdoc.SplitPauseWindows(row.PauseWindows),
		})
	}
	profiles, err := q.ListUserProfiles(ctx)
	if err != nil {
		return nil, err
	}
	profileNames := map[int64]string{}
	for _, row := range profiles {
		user := &doc.Users[userIndex[row.UserID]]
		profileNames[row.ID] = row.Name
		user.Profiles = append(user.Profiles, configdoc.Profile{
			ID: row.ID, Name: row.Name, UUID: row.Uuid, SSPassword: row.SsPassword,
			Remark: row.Remark, DisplayTemplate: row.DisplayTemplate, Tag: row.Tag,
		})
	}

	stacks, err := q.ListStacks(ctx)
	if err != nil {
		return nil, err
	}
	stackIndex := map[int64]int{}
	for _, row := range stacks {
		stack := configdoc.Stack{
			ID: row.ID, Name: row.Name, Enabled: row.Enabled != 0, DesiredRunning: row.DesiredRunning != 0, Role: row.Role,
			DailyLimitBytes: row.DailyLimitBytes, DirectSuffix: row.DirectSuffix,
			Xray: configdoc.Xray{
				AccessLog: row.XrayAccessLog,
				Enabled:   row.XrayEnabled != 0, LogLevel: row.XrayLoglevel, API: row.XrayApi, Stats: row.XrayStats,
				Policy: row.XrayPolicy, APIPort: int(row.XrayApiPort),
				Outbound: configdoc.Outbound{
					Type: row.OutboundType, Server: row.OutboundServer, Port: int(row.OutboundPort),
					Username: row.OutboundUsername, Password: row.OutboundPassword, PrivateDirect: row.OutboundPrivateDirect != 0,
				},
			},
			Mihomo: configdoc.Mihomo{
				Enabled: row.MihomoEnabled != 0, Mode: row.MihomoMode, LogLevel: row.MihomoLoglevel,
				ControllerPort: int(row.ControllerPort), ControllerSecret: row.ControllerSecret,
				Rules: configdoc.Rules{Profile: row.RuleProfile, Final: row.RuleFinal},
			},
		}
		if err := decodeColumn(row.Labels, &stack.Labels); err != nil {
			return nil, err
		}
		if err := decodeColumn(row.RuleExtra, &stack.Mihomo.Rules.Extra); err != nil {
			return nil, err
		}
		stackIndex[row.ID] = len(doc.Stacks)
		doc.Stacks = append(doc.Stacks, stack)
	}

	directUsers, err := q.ListStackDirectUsers(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range directUsers {
		stack := &doc.Stacks[stackIndex[row.StackID]]
		stack.DirectUsers = append(stack.DirectUsers, doc.Users[userIndex[row.UserID]].Name)
	}

	inbounds, err := q.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	type inboundPos struct{ stack, inbound int }
	inboundIndex := map[int64]inboundPos{}
	for _, row := range inbounds {
		position := stackIndex[row.StackID]
		stack := &doc.Stacks[position]
		inbound := configdoc.Inbound{
			ID: row.ID, Name: row.Name, Protocol: row.Protocol, Listen: row.Listen, Port: int(row.Port),
			Sub: row.Sub != 0, UDP: row.Udp != 0, Network: row.Network, WSPath: row.WsPath,
			GRPCServiceName: row.GrpcServiceName, Method: row.Method, Password: row.Password,
			AuthType: row.AuthType, AuthUsername: row.AuthUsername, AuthPassword: row.AuthPassword,
			Users: []configdoc.UserRef{}, Server: row.Server, Remark: row.Remark,
			DisplayTemplate: row.DisplayTemplate, Region: row.Region, Tag: row.Tag,
		}
		if err := decodeColumn(row.WsHeaders, &inbound.WSHeaders); err != nil {
			return nil, err
		}
		inboundIndex[row.ID] = inboundPos{position, len(stack.Xray.Inbounds)}
		stack.Xray.Inbounds = append(stack.Xray.Inbounds, inbound)
	}

	refs, err := q.ListInboundUserRefs(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range refs {
		pos := inboundIndex[row.InboundID]
		inbound := &doc.Stacks[pos.stack].Xray.Inbounds[pos.inbound]
		ref := configdoc.UserRef{
			User:   doc.Users[userIndex[row.UserID]].Name,
			Remark: row.Remark, DisplayTemplate: row.DisplayTemplate, Tag: row.Tag,
		}
		if row.ProfileID.Valid {
			ref.Profile = profileNames[row.ProfileID.Int64]
		}
		inbound.Users = append(inbound.Users, ref)
	}

	listeners, err := q.ListMihomoListeners(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range listeners {
		listener := configdoc.Listener{
			Enabled: row.Enabled != 0, Name: row.Name, Listen: row.Listen, Port: int(row.Port), Auth: row.Auth,
		}
		if err := decodeColumn(row.Accounts, &listener.Accounts); err != nil {
			return nil, err
		}
		stack := &doc.Stacks[stackIndex[row.StackID]]
		if row.Kind == "http" {
			stack.Mihomo.HTTP = listener
		} else {
			stack.Mihomo.Socks = listener
		}
	}

	upstreams, err := q.ListMihomoUpstreams(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range upstreams {
		stack := &doc.Stacks[stackIndex[row.StackID]]
		upstream := configdoc.Upstream{ID: row.ID, Name: row.Name, Type: row.Type}
		if row.RefInboundID.Valid {
			pos, ok := inboundIndex[row.RefInboundID.Int64]
			if !ok {
				return nil, fmt.Errorf("upstream %d references a missing inbound", row.ID)
			}
			target := &doc.Stacks[pos.stack]
			upstream.Stack = target.Name
			upstream.Inbound = target.Xray.Inbounds[pos.inbound].Name
		}
		if row.RawConfig != "" {
			if err := upstream.Config.UnmarshalJSON([]byte(row.RawConfig)); err != nil {
				return nil, err
			}
		}
		stack.Mihomo.Upstreams = append(stack.Mihomo.Upstreams, upstream)
	}

	groups, err := q.ListMihomoGroups(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range groups {
		stack := &doc.Stacks[stackIndex[row.StackID]]
		group := configdoc.Group{
			ID: row.ID, Name: row.Name, Type: row.Type, URL: row.Url, Interval: int(row.Interval), Strategy: row.Strategy,
		}
		if err := decodeColumn(row.Proxies, &group.Proxies); err != nil {
			return nil, err
		}
		stack.Mihomo.Groups = append(stack.Mihomo.Groups, group)
	}
	paused, err := q.ListUserPausedStacks(ctx)
	if err != nil {
		return nil, err
	}
	stackNames := map[int64]string{}
	for _, stack := range doc.Stacks {
		stackNames[stack.ID] = stack.Name
	}
	for _, row := range paused {
		user := &doc.Users[userIndex[row.UserID]]
		user.PausedStacks = append(user.PausedStacks, stackNames[row.StackID])
	}
	if err := loadSubscription(ctx, q, doc, userIndex); err != nil {
		return nil, err
	}
	doc.Normalize()
	return doc, nil
}

func loadSubscription(ctx context.Context, q *dbq.Queries, doc *configdoc.Document, userIndex map[int64]int) error {
	doc.ManualNodes, doc.RemoteSources, doc.Templates = []configdoc.ManualNode{}, []configdoc.RemoteSource{}, &configdoc.Templates{}
	nodes, err := q.ListManualNodes(ctx)
	if err != nil {
		return err
	}
	for _, row := range nodes {
		var node configdoc.ManualNode
		if err := json.Unmarshal([]byte(row.Data), &node); err != nil {
			return fmt.Errorf("manual node %d is corrupt: %w", row.ID, err)
		}
		node.ID, node.User = row.ID, doc.Users[userIndex[row.UserID]].Name
		doc.ManualNodes = append(doc.ManualNodes, node)
	}
	sources, err := q.ListRemoteSources(ctx)
	if err != nil {
		return err
	}
	for _, row := range sources {
		doc.RemoteSources = append(doc.RemoteSources, configdoc.RemoteSource{
			ID: row.ID, Name: row.Name, URL: row.Url, Token: row.Token, Enabled: row.Enabled != 0,
		})
	}
	templates, err := q.ListSubTemplates(ctx)
	if err != nil {
		return err
	}
	for _, row := range templates {
		switch row.Format {
		case "clash":
			doc.Templates.Clash = row.Content
		case "premium":
			doc.Templates.Premium = row.Content
		case "surge":
			doc.Templates.Surge = row.Content
		}
	}
	return nil
}

func saveSubscription(ctx context.Context, q *dbq.Queries, doc *configdoc.Document, userIDByName map[string]int64) error {
	for position := range doc.ManualNodes {
		node := &doc.ManualNodes[position]
		userID, ok := userIDByName[node.User]
		if !ok {
			return fmt.Errorf("manual node references a missing user: %s", node.User)
		}
		stored := *node
		stored.ID, stored.User = 0, ""
		data, err := json.Marshal(stored)
		if err != nil {
			return err
		}
		id, err := q.InsertManualNode(ctx, dbq.InsertManualNodeParams{NULLIF: node.ID, UserID: userID, Position: int64(position), Data: string(data)})
		if err != nil {
			return err
		}
		node.ID = id
	}
	sourceIDs := []int64{}
	for _, source := range doc.RemoteSources {
		if source.ID != 0 {
			sourceIDs = append(sourceIDs, source.ID)
		}
	}
	if err := deleteExcept(ctx, sourceIDs, q.DeleteRemoteSourcesExcept, q.DeleteAllRemoteSources); err != nil {
		return err
	}
	for position := range doc.RemoteSources {
		source := &doc.RemoteSources[position]
		id, err := q.UpsertRemoteSource(ctx, dbq.UpsertRemoteSourceParams{
			NULLIF: source.ID, Position: int64(position), Name: source.Name, Url: source.URL, Token: source.Token, Enabled: boolInt(source.Enabled),
		})
		if err != nil {
			return fmt.Errorf("save remote source %s: %w", source.Name, err)
		}
		source.ID = id
	}
	if doc.Templates != nil {
		for format, content := range map[string]string{"clash": doc.Templates.Clash, "premium": doc.Templates.Premium, "surge": doc.Templates.Surge} {
			var err error
			if content == "" {
				err = q.DeleteSubTemplate(ctx, format)
			} else {
				err = q.PutSubTemplate(ctx, dbq.PutSubTemplateParams{Format: format, Content: content})
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// SaveDocument 把文档整体写入配置表，新实体的 ID 回填到文档。调用方负责事务和校验。
func SaveDocument(ctx context.Context, q *dbq.Queries, doc *configdoc.Document) error {
	if doc.Settings != nil {
		if err := SaveSettings(ctx, q, *doc.Settings); err != nil {
			return err
		}
	}
	for _, clear := range []func(context.Context) error{
		q.DeleteStackDirectUsers,
		q.DeleteUserPausedStacks, q.DeleteManualNodes, q.DeleteMihomoUpstreams, q.DeleteMihomoGroups, q.DeleteMihomoListeners,
		q.DeleteInboundUserRefs, q.DeleteInbounds, q.DeleteUserProfiles,
	} {
		if err := clear(ctx); err != nil {
			return err
		}
	}
	userIDs := make([]int64, 0, len(doc.Users))
	for _, user := range doc.Users {
		if user.ID != 0 {
			userIDs = append(userIDs, user.ID)
		}
	}
	if err := deleteExcept(ctx, userIDs, q.DeleteUsersExcept, q.DeleteAllUsers); err != nil {
		return err
	}
	stackIDs := make([]int64, 0, len(doc.Stacks))
	for _, stack := range doc.Stacks {
		if stack.ID != 0 {
			stackIDs = append(stackIDs, stack.ID)
		}
	}
	if err := deleteExcept(ctx, stackIDs, q.DeleteStacksExcept, q.DeleteAllStacks); err != nil {
		return err
	}

	userIDByName := map[string]int64{}
	profileIDs := map[string]int64{}
	for index := range doc.Users {
		user := &doc.Users[index]
		id, err := q.UpsertUser(ctx, dbq.UpsertUserParams{
			NULLIF: user.ID, Position: int64(index), Name: user.Name, Enabled: boolInt(user.Enabled),
			Uuid: user.UUID, SsPassword: user.SSPassword, Remark: user.Remark, DisplayTemplate: user.DisplayTemplate,
			Tag: user.Tag, SubToken: user.SubToken, QuotaBytes: user.QuotaBytes, DailyLimitBytes: user.DailyLimitBytes,
			DailyLimitAction: user.DailyLimitAction, ResetMode: user.ResetMode,
			ResetDay: int64(user.ResetDay), ExpireAt: user.ExpireAt, Note: user.Note,
			PauseWindowMode: user.PauseWindowMode, PauseWindows: configdoc.JoinPauseWindows(user.PauseWindows),
		})
		if err != nil {
			return fmt.Errorf("save user %s: %w", user.Name, err)
		}
		user.ID = id
		userIDByName[user.Name] = id
		for position := range user.Profiles {
			profile := &user.Profiles[position]
			profileID, err := q.InsertUserProfile(ctx, dbq.InsertUserProfileParams{
				NULLIF: profile.ID, UserID: id, Position: int64(position), Name: profile.Name,
				Uuid: profile.UUID, SsPassword: profile.SSPassword, Remark: profile.Remark,
				DisplayTemplate: profile.DisplayTemplate, Tag: profile.Tag,
			})
			if err != nil {
				return fmt.Errorf("save profile %s/%s: %w", user.Name, profile.Name, err)
			}
			profile.ID = profileID
			profileIDs[user.Name+"\x00"+profile.Name] = profileID
		}
	}

	inboundIDs := map[string]int64{}
	for index := range doc.Stacks {
		stack := &doc.Stacks[index]
		labels, err := encodeColumn(stack.Labels)
		if err != nil {
			return err
		}
		ruleExtra, err := encodeColumn(stack.Mihomo.Rules.Extra)
		if err != nil {
			return err
		}
		xray, mihomo, outbound := stack.Xray, stack.Mihomo, stack.Xray.Outbound
		id, err := q.UpsertStack(ctx, dbq.UpsertStackParams{
			NULLIF: stack.ID, Position: int64(index), Name: stack.Name, Enabled: boolInt(stack.Enabled),
			DesiredRunning: boolInt(stack.DesiredRunning), Role: stack.Role, Labels: labels, DailyLimitBytes: stack.DailyLimitBytes,
			DirectSuffix: stack.DirectSuffix,
			XrayEnabled: boolInt(xray.Enabled), XrayLoglevel: xray.LogLevel, XrayApi: xray.API,
			XrayAccessLog: xray.AccessLog,
			XrayStats:     xray.Stats, XrayPolicy: xray.Policy, XrayApiPort: int64(xray.APIPort),
			OutboundType: outbound.Type, OutboundServer: outbound.Server, OutboundPort: int64(outbound.Port),
			OutboundUsername: outbound.Username, OutboundPassword: outbound.Password,
			OutboundPrivateDirect: boolInt(outbound.PrivateDirect), MihomoEnabled: boolInt(mihomo.Enabled),
			MihomoMode: mihomo.Mode, MihomoLoglevel: mihomo.LogLevel, ControllerPort: int64(mihomo.ControllerPort),
			ControllerSecret: mihomo.ControllerSecret, RuleProfile: mihomo.Rules.Profile,
			RuleFinal: mihomo.Rules.Final, RuleExtra: ruleExtra,
		})
		if err != nil {
			return fmt.Errorf("save stack %s: %w", stack.Name, err)
		}
		stack.ID = id
		for position, user := range stack.DirectUsers {
			userID, ok := userIDByName[user]
			if !ok {
				return fmt.Errorf("stack %s references a missing direct user: %s", stack.Name, user)
			}
			if err := q.InsertStackDirectUser(ctx, dbq.InsertStackDirectUserParams{StackID: id, UserID: userID, Position: int64(position)}); err != nil {
				return err
			}
		}
		for position := range stack.Xray.Inbounds {
			inbound := &stack.Xray.Inbounds[position]
			headers, err := encodeColumn(inbound.WSHeaders)
			if err != nil {
				return err
			}
			inboundID, err := q.InsertInbound(ctx, dbq.InsertInboundParams{
				NULLIF: inbound.ID, StackID: id, Position: int64(position), Name: inbound.Name,
				Protocol: inbound.Protocol, Listen: inbound.Listen, Port: int64(inbound.Port), Sub: boolInt(inbound.Sub),
				Udp: boolInt(inbound.UDP), Network: inbound.Network, WsPath: inbound.WSPath, WsHeaders: headers,
				GrpcServiceName: inbound.GRPCServiceName, Method: inbound.Method, Password: inbound.Password,
				AuthType: inbound.AuthType, AuthUsername: inbound.AuthUsername, AuthPassword: inbound.AuthPassword,
				Server: inbound.Server, Remark: inbound.Remark, DisplayTemplate: inbound.DisplayTemplate,
				Region: inbound.Region, Tag: inbound.Tag,
			})
			if err != nil {
				return fmt.Errorf("save inbound %s.%s: %w", stack.Name, inbound.Name, err)
			}
			inbound.ID = inboundID
			inboundIDs[stack.Name+"\x00"+inbound.Name] = inboundID
			for refPosition, ref := range inbound.Users {
				userID, ok := userIDByName[ref.User]
				if !ok {
					return fmt.Errorf("inbound %s.%s references a missing user: %s", stack.Name, inbound.Name, ref.User)
				}
				profileID := sql.NullInt64{}
				if ref.Profile != "" && ref.Profile != configdoc.DefaultProfile {
					value, ok := profileIDs[ref.User+"\x00"+ref.Profile]
					if !ok {
						return fmt.Errorf("inbound %s.%s references a missing profile: %s/%s", stack.Name, inbound.Name, ref.User, ref.Profile)
					}
					profileID = sql.NullInt64{Int64: value, Valid: true}
				}
				if err := q.InsertInboundUserRef(ctx, dbq.InsertInboundUserRefParams{
					InboundID: inboundID, Position: int64(refPosition), UserID: userID, ProfileID: profileID,
					Remark: ref.Remark, DisplayTemplate: ref.DisplayTemplate, Tag: ref.Tag,
				}); err != nil {
					return err
				}
			}
		}
		for kind, listener := range map[string]configdoc.Listener{"socks": mihomo.Socks, "http": mihomo.HTTP} {
			accounts, err := encodeColumn(listener.Accounts)
			if err != nil {
				return err
			}
			if err := q.InsertMihomoListener(ctx, dbq.InsertMihomoListenerParams{
				StackID: id, Kind: kind, Enabled: boolInt(listener.Enabled), Name: listener.Name,
				Listen: listener.Listen, Port: int64(listener.Port), Auth: listener.Auth, Accounts: accounts,
			}); err != nil {
				return err
			}
		}
		for position := range stack.Mihomo.Groups {
			group := &stack.Mihomo.Groups[position]
			proxies, err := encodeColumn(group.Proxies)
			if err != nil {
				return err
			}
			groupID, err := q.InsertMihomoGroup(ctx, dbq.InsertMihomoGroupParams{
				NULLIF: group.ID, StackID: id, Position: int64(position), Name: group.Name, Type: group.Type,
				Proxies: proxies, Url: group.URL, Interval: int64(group.Interval), Strategy: group.Strategy,
			})
			if err != nil {
				return err
			}
			group.ID = groupID
		}
	}
	if err := saveSubscription(ctx, q, doc, userIDByName); err != nil {
		return err
	}
	stackIDByName := map[string]int64{}
	for _, stack := range doc.Stacks {
		stackIDByName[stack.Name] = stack.ID
	}
	for _, user := range doc.Users {
		for _, name := range user.PausedStacks {
			stackID, ok := stackIDByName[name]
			if !ok {
				return fmt.Errorf("user %s pauses a missing stack: %s", user.Name, name)
			}
			if err := q.InsertUserPausedStack(ctx, dbq.InsertUserPausedStackParams{UserID: user.ID, StackID: stackID}); err != nil {
				return err
			}
		}
	}
	// 上游可能引用后面才写入的 stack 的入站，所以在全部入站写入之后再写。
	for index := range doc.Stacks {
		stack := &doc.Stacks[index]
		for position := range stack.Mihomo.Upstreams {
			upstream := &stack.Mihomo.Upstreams[position]
			refInbound := sql.NullInt64{}
			rawConfig := ""
			switch upstream.Type {
			case "xray-socks5":
				value, ok := inboundIDs[upstream.Stack+"\x00"+upstream.Inbound]
				if !ok {
					return fmt.Errorf("upstream %s.%s references a missing inbound: %s.%s", stack.Name, upstream.Name, upstream.Stack, upstream.Inbound)
				}
				refInbound = sql.NullInt64{Int64: value, Valid: true}
			case "raw":
				data, err := upstream.Config.MarshalJSON()
				if err != nil {
					return err
				}
				rawConfig = string(data)
			}
			upstreamID, err := q.InsertMihomoUpstream(ctx, dbq.InsertMihomoUpstreamParams{
				NULLIF: upstream.ID, StackID: stack.ID, Position: int64(position), Name: upstream.Name,
				Type: upstream.Type, RefInboundID: refInbound, RawConfig: rawConfig,
			})
			if err != nil {
				return err
			}
			upstream.ID = upstreamID
		}
	}
	return saveTrafficNames(ctx, q, doc)
}

// saveTrafficNames 记下用户和 stack 的当前名字与角色。删除后记录保留，历史流量仍能显示名字。
func saveTrafficNames(ctx context.Context, q *dbq.Queries, doc *configdoc.Document) error {
	for _, user := range doc.Users {
		if err := q.UpsertTrafficName(ctx, dbq.UpsertTrafficNameParams{Kind: "user", ID: user.ID, Name: user.Name}); err != nil {
			return err
		}
	}
	for _, stack := range doc.Stacks {
		if err := q.UpsertTrafficName(ctx, dbq.UpsertTrafficNameParams{Kind: "stack", ID: stack.ID, Name: stack.Name, Role: stack.Role}); err != nil {
			return err
		}
	}
	return nil
}

func deleteExcept(ctx context.Context, ids []int64, except func(context.Context, []int64) error, all func(context.Context) error) error {
	if len(ids) == 0 {
		return all(ctx)
	}
	return except(ctx, ids)
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func encodeColumn(value any) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}

func decodeColumn(data string, target any) error {
	if data == "" {
		return nil
	}
	return json.Unmarshal([]byte(data), target)
}
