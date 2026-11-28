package api

// viewerOperationAllowed 显式列出只读运维可以调用的操作；其他操作默认拒绝。
// 自己的密码、两步验证和会话管理独立于运维权限；凭据查看、配置预览和备份下载仅限管理员。
func viewerOperationAllowed(operation string) bool {
	switch operation {
	case "getSession", "logout", "changePassword", "beginTOTP", "enableTOTP", "disableTOTP", "listActiveSessions", "revokeSession",
		"getSystemInfo", "getSettings", "listUsers", "getUser", "listStacks", "getStack", "checkPort", "listRevisions",
		"listManualNodes", "listRemoteSources", "getTemplates", "getSubscriptionBundleStatus", "getSSHPublishIdentity", "listSubscriptionPublishTargets",
		"getDeps", "listDepReleases", "checkSelfUpdate", "getSelfUpgradeSchedule",
		"getPending", "getRuntimeStatus", "listTasks", "getTask", "listTaskCenter", "getTaskCenterDetail", "events", "streamLogs",
		"getAccessFavicon", "listManualIcons", "getAccessStatus", "listAccessDomains", "listAccessDomainUsers", "listUserAccessDomains", "exportAccessDomains",
		"getTrafficOverview", "getTrafficStats", "exportTrafficStats", "getTrafficRanking", "getPauseWindowStatus", "listUsersUsage", "getStackUsage", "getUserUsage", "listNotifications",
		"listJobs", "listJobRuns", "listBackups", "getRestoreStatus", "getHealthChecks", "getStackEgressHealth", "getStackEgress", "listStackProxies", "listAudit",
		"listProviderNodes", "listStackProviders":
		return true
	}
	return false
}
