package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/eagle/proxystack-web/internal/app"
	"github.com/eagle/proxystack-web/internal/paths"
	"github.com/eagle/proxystack-web/internal/store"
)

// openService 打开已初始化实例的数据库；未初始化时报错。
func openService(ctx context.Context, layout paths.Layout) (*app.Service, func(), error) {
	if _, err := os.Stat(layout.DBPath()); err != nil {
		return nil, nil, app.ErrNotInitialized
	}
	st, err := store.Open(layout.DBPath())
	if err != nil {
		return nil, nil, err
	}
	svc := app.New(st, layout)
	ok, err := svc.IsInitialized(ctx)
	if err != nil || !ok {
		st.Close()
		if err == nil {
			err = app.ErrNotInitialized
		}
		return nil, nil, err
	}
	closeFn := func() {
		st.Close()
		fixOwnership(layout)
	}
	return svc, closeFn, nil
}

// fixOwnership 在以 root 执行救援命令后，把数据库文件属主改回数据目录的属主，
// 避免 WAL/SHM 文件变为 root 所有导致服务无法写库。
func fixOwnership(layout paths.Layout) {
	if os.Geteuid() != 0 {
		return
	}
	info, err := os.Stat(layout.DataDir())
	if err != nil {
		return
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	matches, _ := filepath.Glob(layout.DBPath() + "*")
	for _, name := range matches {
		if err := os.Lchown(name, int(st.Uid), int(st.Gid)); err != nil {
			fmt.Fprintf(os.Stderr, "warning: chown %s: %v\n", name, err)
		}
	}
}
