package download

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

func acquireTaskLock(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(dir, "download.lock"))
	ok, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("这个任务已经在另一个进程中下载")
	}
	return func() { _ = lock.Close() }, nil
}
