//go:build unix

package subserver

import (
	"os"
	"syscall"
)

// matchOwner 在以 root 执行命令（导入、改配置）时，把新文件属主改为所在目录的属主，订阅端服务账号才能读取。
func matchOwner(path, dir string) {
	if os.Geteuid() != 0 {
		return
	}
	info, err := os.Stat(dir)
	if err != nil {
		return
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		_ = os.Lchown(path, int(st.Uid), int(st.Gid))
	}
}
