//go:build unix

package subserver

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// acquireImportLock 跨进程串行化订阅包的版本检查和替换。
func acquireImportLock(layout Layout) (func(), error) {
	file, err := os.OpenFile(filepath.Join(layout.DataDir(), ".import.lock"), os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		return nil, err
	}
	matchOwner(file.Name(), layout.DataDir())
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}, nil
}
