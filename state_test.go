package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInputPromptCanBeCancelled(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := readLineContext(ctx, reader)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTaskLockReleasesWithoutDeletingDownloads(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireTaskLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if release2, err := acquireTaskLock(dir); err == nil {
		release2()
		t.Fatal("duplicate writer acquired lock")
	}
	marker := filepath.Join(dir, "keep.iso")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	release()
	release, err = acquireTaskLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("lock cleanup removed existing payload")
	}
}
