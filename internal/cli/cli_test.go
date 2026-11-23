package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestRunVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"--version"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if out.String() != "P2PGet 0.1.1\n" {
		t.Fatalf("version output %q", out.String())
	}
}

func TestRunRejectsTwoLinks(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"a", "b"}, strings.NewReader(""), &out, &errOut); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

func TestRepeatedFlagsAccumulate(t *testing.T) {
	var servers []string
	f := (*stringFlags)(&servers)
	f.Set("a:1")
	f.Set("b:2")
	if len(servers) != 2 || servers[0] != "a:1" || servers[1] != "b:2" {
		t.Fatalf("got %v", servers)
	}
}
