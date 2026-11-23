package main

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

func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".save-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
