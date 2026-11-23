package main

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

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	goed2k "github.com/goed2k/core"
	"github.com/qiuxsgit/p2pget/internal/engine"
	"github.com/qiuxsgit/p2pget/internal/link"
	"golang.org/x/time/rate"
)

func testOptions(dir string) engine.Options {
	return engine.Options{Dir: dir, Connections: 30, UploadKiB: 256, NoDiscovery: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestBTPartialDownloadAndRestart(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	os.MkdirAll(seedDir, 0755)
	payload := bytes.Repeat([]byte("portable-p2p-fixture"), 65536)
	seedFile := filepath.Join(seedDir, "fixture.bin")
	if err := os.WriteFile(seedFile, payload, 0600); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 16384}
	if err := info.BuildFromFilePath(seedFile); err != nil {
		t.Fatal(err)
	}
	encoded, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: encoded}
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = seedDir
	cfg.ListenPort = 0
	cfg.ListenHost = func(string) string { return "127.0.0.1" }
	cfg.DisableIPv6 = true
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.NoDefaultPortForwarding = true
	cfg.Seed = true
	cfg.Slogger = testOptions("").Logger
	cfg.UploadRateLimiter = rate.NewLimiter(64*1024, 64*1024)
	seed, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	seedTorrent, err := seed.AddTorrent(&mi)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = seedTorrent.VerifyDataContext(ctx); err != nil {
		t.Fatal(err)
	}
	req, err := link.Parse("magnet:?xt=urn:btih:" + mi.HashInfoBytes().HexString())
	if err != nil {
		t.Fatal(err)
	}
	opts := testOptions(filepath.Join(root, "download"))
	opts.Peers = []string{seed.ListenAddrs()[0].String()}
	task := filepath.Join(opts.Dir, "bt-"+req.ID)
	state := filepath.Join(task, ".p2pget")
	os.MkdirAll(state, 0755)
	job, err := newBTJob(req, opts, task, state)
	if err != nil {
		t.Fatal(err)
	}
	partial := int64(0)
	for ctx.Err() == nil {
		p, err := job.Progress()
		if err != nil {
			job.Close()
			t.Fatal(err)
		}
		if p.Done > 0 && p.Done < p.Total {
			partial = p.Done
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err = job.Close(); err != nil {
		t.Fatal(err)
	}
	if partial == 0 {
		t.Fatal("did not receive any verified partial data")
	}
	cfg.UploadRateLimiter.SetLimit(rate.Inf)
	var output bytes.Buffer
	if err = download(ctx, req, opts, &output); err != nil {
		t.Fatalf("restart: %v\n%s", err, output.String())
	}
	actual, err := os.ReadFile(filepath.Join(task, "fixture.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, payload) {
		t.Fatal("BT payload mismatch")
	}
	// A second restart without a supplied peer verifies existing data and completes locally.
	opts.Peers = nil
	if err = download(ctx, req, opts, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestED2KRealPeerTransferAndRestart(t *testing.T) {
	root := t.TempDir()
	payload := bytes.Repeat([]byte("ed2k-real-peer-fixture"), 24000)
	seedPath := filepath.Join(root, "fixture.bin")
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
	settings.Logger = testOptions("").Logger
	settings.EnableUPnP = false
	seed := goed2k.NewClient(settings)
	if err = startEDSession(seed, false); err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	if err = seed.ImportSharedFile(seedPath); err != nil {
		t.Fatal(err)
	}
	seedPort := seed.Session().GetListenPort()
	seedStop := make(chan struct{})
	seedDone := make(chan struct{})
	go func() {
		defer close(seedDone)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		last := time.Now()
		for {
			select {
			case now := <-tick.C:
				goed2k.UpdateCachedTime()
				seed.Session().SecondTick(goed2k.CurrentTime(), max(1, now.Sub(last).Milliseconds()))
				last = now
			case <-seedStop:
				return
			}
		}
	}()
	defer func() { close(seedStop); <-seedDone; seed.Session().FlushDiskTasks() }()

	req, err := link.Parse(goed2k.FormatLink("fixture.bin", size, hash))
	if err != nil {
		t.Fatal(err)
	}
	opts := testOptions(filepath.Join(root, "download"))
	opts.Peers = []string{fmt.Sprintf("127.0.0.1:%d", seedPort)}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var output bytes.Buffer
	if err = download(ctx, req, opts, &output); err != nil {
		t.Fatalf("ED2K transfer: %v\n%s", err, output.String())
	}
	target := filepath.Join(opts.Dir, "ed2k-"+req.ID, "fixture.bin")
	actual, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, payload) {
		t.Fatal("ED2K payload mismatch")
	}
	opts.Peers = nil
	if err = download(ctx, req, opts, io.Discard); err != nil {
		t.Fatalf("ED2K restart failed: %v", err)
	}
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
	if err = startEDSession(seed, false); err != nil {
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
	task := filepath.Join(opts.Dir, "ed2k-"+req.ID)
	state := filepath.Join(task, ".p2pget")
	os.MkdirAll(state, 0755)
	job, err := newED2KJob(ctx, req, opts, task, state)
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
	job, err = newED2KJob(ctx, req, opts, task, state)
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
	secondTask := filepath.Join(secondOpts.Dir, "ed2k-"+req.ID)
	secondState := filepath.Join(secondTask, ".p2pget")
	os.MkdirAll(secondState, 0755)
	second, err := newED2KJob(ctx, req, secondOpts, secondTask, secondState)
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
