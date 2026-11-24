package ed2k

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	goed2k "github.com/goed2k/core"
	"github.com/qiuxsgit/p2pget/internal/engine"
	"github.com/qiuxsgit/p2pget/internal/link"
)

func testOptions(dir string) engine.Options {
	return engine.Options{Dir: dir, Connections: 30, UploadKiB: 256, NoDiscovery: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestED2KPartialResumeWhileUploading(t *testing.T) {
	root := t.TempDir()
	payload := bytes.Repeat([]byte("multi-part-transfer-fixture-"), (2*int(goed2k.PieceSize)+262144)/28+1)
	seedPath := filepath.Join(root, "large.bin")
	if err := os.WriteFile(seedPath, payload, 0600); err != nil {
		t.Fatal(err)
	}
	hash, size, _, err := goed2k.ComputeEd2kFileMeta(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	settings := goed2k.NewSettings()
	settings.ListenPort = 0
	settings.UDPPort = 0
	settings.MaxUploadRateKB = 8192
	settings.Logger = testOptions("").Logger
	seed := goed2k.NewClient(settings)
	if err = StartSession(seed, false); err != nil {
		t.Fatal(err)
	}
	if err = seed.ImportSharedFile(seedPath); err != nil {
		t.Fatal(err)
	}
	seedAddress := fmt.Sprintf("127.0.0.1:%d", seed.Session().GetListenPort())
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		last := time.Now()
		for {
			select {
			case now := <-tick.C:
				goed2k.UpdateCachedTime()
				seed.Session().SecondTick(goed2k.CurrentTime(), max(1, now.Sub(last).Milliseconds()))
				last = now
			case <-stop:
				return
			}
		}
	}()
	defer func() { close(stop); <-done; seed.Session().FlushDiskTasks(); seed.Close() }()
	req, err := link.Parse(goed2k.FormatLink("large.bin", size, hash))
	if err != nil {
		t.Fatal(err)
	}
	opts := testOptions(filepath.Join(root, "first"))
	opts.UploadKiB = 4096
	opts.Peers = []string{seedAddress}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	state := filepath.Join(opts.Dir, ".p2pget", "ed2k-"+req.ID)
	os.MkdirAll(state, 0755)
	job, err := New(ctx, req, opts, state)
	if err != nil {
		t.Fatal(err)
	}
	interrupted := false
	for ctx.Err() == nil {
		p, err := job.Progress()
		if err != nil {
			job.Close()
			t.Fatal(err)
		}
		if p.Done >= goed2k.PieceSize && p.Done < p.Total {
			interrupted = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = job.Close(); err != nil {
		t.Fatal(err)
	}
	if !interrupted {
		t.Fatal("did not observe an interrupted multi-part download")
	}
	job, err = New(ctx, req, opts, state)
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	if p, err := job.Progress(); err != nil || p.Done < goed2k.PieceSize {
		t.Fatalf("verified first part did not resume: %+v %v", p, err)
	}
	secondOpts := testOptions(filepath.Join(root, "second"))
	secondOpts.UploadKiB = 4096
	secondOpts.Peers = []string{fmt.Sprintf("127.0.0.1:%d", job.client.Session().GetListenPort())}
	secondState := filepath.Join(secondOpts.Dir, ".p2pget", "ed2k-"+req.ID)
	os.MkdirAll(secondState, 0755)
	second, err := New(ctx, req, secondOpts, secondState)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	firstDone, secondDone := false, false
	for ctx.Err() == nil {
		a, err := job.Progress()
		if err != nil {
			t.Fatal(err)
		}
		b, err := second.Progress()
		if err != nil {
			t.Fatal(err)
		}
		firstDone = a.Phase == "已完成"
		secondDone = b.Phase == "已完成"
		if firstDone && secondDone {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !firstDone || !secondDone {
		t.Fatalf("multi-part transfer timed out: first=%v second=%v, A=%s, B=%s, peersA=%+v, peersB=%+v", firstDone, secondDone, job.handle.GetStatus().String(), second.handle.GetStatus().String(), job.handle.GetPeersInfo(), second.handle.GetPeersInfo())
	}
	if err = job.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	if err = second.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	if job.handle.GetStatus().Upload == 0 {
		t.Fatal("resumed downloader did not upload any blocks to the second downloader")
	}
}
