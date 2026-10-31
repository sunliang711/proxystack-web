// Package version 保存构建时注入的版本信息。
package version

import "runtime/debug"

// 由 -ldflags "-X" 在构建时注入。
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Info 是当前二进制的版本信息，构建时注入的字段优先于 Go 内嵌的 VCS 信息。
type Info struct {
	Version string
	Commit  string
	Date    string
}

// Current 返回版本信息，普通 go build 未注入版本时补充提交号和未提交修改标记。
func Current() Info {
	buildInfo, _ := debug.ReadBuildInfo()
	return resolve(Info{Version: Version, Commit: Commit, Date: Date}, buildInfo)
}

func resolve(info Info, buildInfo *debug.BuildInfo) Info {
	modified := false
	if buildInfo != nil {
		for _, setting := range buildInfo.Settings {
			switch setting.Key {
			case "vcs.revision":
				if info.Commit == "" || info.Commit == "unknown" {
					info.Commit = setting.Value
				}
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}
	}
	if info.Commit == "" {
		info.Commit = "unknown"
	}
	// vcs.time 是提交时间，不能用它冒充构建时间。
	if info.Date == "" {
		info.Date = "unknown"
	}
	if info.Version == "" || info.Version == "dev" || info.Version == "unknown" {
		info.Version = "dev"
		if info.Commit != "unknown" {
			const shortCommitLength = 7
			commit := info.Commit
			if len(commit) > shortCommitLength {
				commit = commit[:shortCommitLength]
			}
			info.Version += "+" + commit
		}
		if modified {
			if info.Version == "dev" {
				info.Version += "+dirty"
			} else {
				info.Version += ".dirty"
			}
		}
	}
	return info
}
