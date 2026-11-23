// Package bt downloads torrents and magnet links with anacrolix/torrent.
package bt

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"

	g "github.com/anacrolix/generics"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
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
	metadataPath string
	prepared     bool
}

func New(req link.Request, opts engine.Options, taskDir, stateDir string) (*Job, error) {
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = taskDir
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
	st := storage.NewFileOpts(storage.NewFileClientOpts{ClientBaseDir: taskDir, PieceCompletion: storage.NewMapPieceCompletion(), UsePartFiles: g.Some(false), DisableMmap: true, Logger: opts.Logger})
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
	return &Job{client: client, torrent: t, storage: st, metadataPath: metadataPath}, nil
}

func (j *Job) Progress() (engine.Progress, error) {
	t := j.torrent
	select {
	case <-t.GotInfo():
		if !j.prepared {
			if err := link.ValidateTorrentInfo(t.Info()); err != nil {
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
