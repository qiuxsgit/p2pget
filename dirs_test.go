package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDownloadDirSurvivesGoRun(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := defaultDownloadDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(cwd, "downloads") {
		t.Fatalf("default directory is not relative to the working directory: %s", got)
	}
}
