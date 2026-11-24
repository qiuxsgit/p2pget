// Package bt downloads torrents and magnet links with anacrolix/torrent.
package bt

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"slices"

	g "github.com/anacrolix/generics"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/qiuxsgit/p2pget/internal/claim"
	"github.com/qiuxsgit/p2pget/internal/engine"
	"github.com/qiuxsgit/p2pget/internal/fsutil"
	"github.com/qiuxsgit/p2pget/internal/link"
	"golang.org/x/time/rate"
)

var defaultTrackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.demonii.com:1337/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://open.stealth.si:80/announce",
	"udp://exodus.desync.com:6969/announce",
	"http://tracker.opentrackr.org:1337/announce",
}

type Job struct {
	client       *torrent.Client
	torrent      *torrent.Torrent
	storage      storage.ClientImplCloser
	root         string
	stateDir     string
	metadataPath string
	prepared     bool
}

// filePath places a single-file torrent's file directly in the download root and a
// multi-file torrent's files under a directory named after the torrent.
func filePath(opts storage.FilePathMakerOpts) string {
	name := opts.Info.BestName()
	path := opts.File.BestPath()
	if isSingleFile(opts.Info) {
		return name
	}
	if name == metainfo.NoName {
		return filepath.Join(path...)
	}
	return filepath.Join(append([]string{name}, path...)...)
}

// isSingleFile reports whether info describes one file. Info.IsDir is always true for
// v2 infos, whose file tree names the single file inside the root directory.
func isSingleFile(info *metainfo.Info) bool {
	files := info.UpvertedFiles()
	if len(files) != 1 {
		return false
	}
	path := files[0].BestPath()
	return len(path) == 0 || (len(path) == 1 && path[0] == info.BestName())
}

func New(req link.Request, opts engine.Options, stateDir string) (*Job, error) {
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = opts.Dir
	cfg.ListenPort = 0
	cfg.NoDefaultPortForwarding = true
	cfg.Seed = false
	cfg.NoDHT = opts.NoDiscovery
	cfg.DisableTrackers = opts.NoDiscovery
	cfg.DisablePEX = opts.NoDiscovery
	cfg.Slogger = opts.Logger
	cfg.HalfOpenConnsPerTorrent = min(60, opts.Connections)
	cfg.TotalHalfOpenConns = min(60, opts.Connections)
	if opts.UploadKiB > 0 {
		cfg.UploadRateLimiter = rate.NewLimiter(rate.Limit(opts.UploadKiB*1024), max(opts.UploadKiB*1024, 64*1024))
	}
	st := storage.NewFileOpts(storage.NewFileClientOpts{ClientBaseDir: opts.Dir, FilePathMaker: filePath, PieceCompletion: storage.NewMapPieceCompletion(), UsePartFiles: g.Some(false), DisableMmap: true, Logger: opts.Logger})
	cfg.DefaultStorage = st
	client, err := torrent.NewClient(cfg)
	if err != nil {
		st.Close()
		return nil, err
	}
	spec := *req.Spec
	// The cached torrent contains metadata, not a trusted completion bitmap. Existing data is rehashed.
	metadataPath := filepath.Join(stateDir, "metadata.torrent")
	if mi, err := metainfo.LoadFromFile(metadataPath); err == nil {
		if cached, err := torrent.TorrentSpecFromMetaInfoErr(mi); err == nil {
			if r, err := link.FromTorrentSpec(cached, req.Raw); err == nil && r.ID == req.ID {
				cached.Trackers = append(cached.Trackers, spec.Trackers...)
				cached.PeerAddrs = append(cached.PeerAddrs, spec.PeerAddrs...)
				spec = *cached
			}
		}
	}
	if len(spec.Trackers) == 0 || (len(spec.Trackers) == 1 && len(spec.Trackers[0]) == 0) {
		for _, tr := range defaultTrackers {
			spec.Trackers = append(spec.Trackers, []string{tr})
		}
	}
	for _, tr := range opts.Trackers {
		spec.Trackers = append(spec.Trackers, []string{tr})
	}
	spec.PeerAddrs = append(spec.PeerAddrs, opts.Peers...)
	spec.DisallowDataDownload = true
	spec.DeferStorageOpen = true
	t, _, err := client.AddTorrentSpec(&spec)
	if err != nil {
		client.Close()
		st.Close()
		return nil, err
	}
	t.SetMaxEstablishedConns(opts.Connections)
	return &Job{client: client, torrent: t, storage: st, root: opts.Dir, stateDir: stateDir, metadataPath: metadataPath}, nil
}

func (j *Job) targets(info *metainfo.Info) []string {
	var targets []string
	for _, file := range info.UpvertedFiles() {
		target := filepath.Join(j.root, filePath(storage.FilePathMakerOpts{Info: info, File: &file}))
		// Same-length padding files may share a path.
		if !slices.Contains(targets, target) {
			targets = append(targets, target)
		}
	}
	return targets
}

// Path returns the downloaded file, or the torrent's directory for multi-file torrents.
// It is empty until the torrent info is known.
func (j *Job) Path() string {
	select {
	case <-j.torrent.GotInfo():
	default:
		return ""
	}
	info := j.torrent.Info()
	if !isSingleFile(info) && info.BestName() == metainfo.NoName {
		return j.root
	}
	return filepath.Join(j.root, info.BestName())
}

func (j *Job) Progress() (engine.Progress, error) {
	t := j.torrent
	select {
	case <-t.GotInfo():
		if !j.prepared {
			if err := link.ValidateTorrentInfo(t.Info()); err != nil {
				return engine.Progress{}, err
			}
			if err := claim.Claim(j.root, j.stateDir, j.targets(t.Info())); err != nil {
				return engine.Progress{}, err
			}
			mi := t.Metainfo()
			var buf bytes.Buffer
			if err := mi.Write(&buf); err != nil {
				return engine.Progress{}, err
			}
			if err := fsutil.AtomicWrite(j.metadataPath, buf.Bytes()); err != nil {
				return engine.Progress{}, err
			}
			if err := t.OpenStorage(context.Background(), nil); err != nil {
				return engine.Progress{}, err
			}
			t.AllowDataDownload()
			t.DownloadAll()
			j.prepared = true
		}
	default:
		s := t.Stats()
		return engine.Progress{Phase: "获取种子", Peers: s.ActivePeers, Known: s.TotalPeers, Received: s.BytesReadData.Int64()}, nil
	}
	s := t.Stats()
	p := engine.Progress{Phase: "下载 / 校验", Done: t.BytesCompleted(), Total: t.Length(), Received: s.BytesReadData.Int64(), Peers: s.ActivePeers, Known: s.TotalPeers}
	if t.Complete().Bool() {
		p.Phase = "已完成"
	}
	return p, nil
}

func (j *Job) Verify(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !j.torrent.Complete().Bool() {
		return fmt.Errorf("BT 校验未完成")
	}
	return nil
}
func (j *Job) Close() error {
	errs := j.client.Close()
	err := j.storage.Close()
	if len(errs) > 0 {
		return fmt.Errorf("关闭 BT 内核：%v；存储：%v", errs, err)
	}
	return err
}
