//go:build !unix

package subserver

import "sync"

var importLock sync.Mutex

// acquireImportLock 在非 Unix 平台提供进程内串行化；生产订阅端运行在 Unix。
func acquireImportLock(_ Layout) (func(), error) {
	importLock.Lock()
	return importLock.Unlock, nil
}
