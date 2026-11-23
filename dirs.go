package main

import (
	"os"
	"path/filepath"
)

func defaultDownloadDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, "downloads"), nil
}
