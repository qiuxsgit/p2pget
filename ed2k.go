package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	goed2k "github.com/goed2k/core"
	"github.com/goed2k/core/disk"
	"github.com/goed2k/core/protocol"
	"github.com/qiuxsgit/p2pget/internal/engine"
	"github.com/qiuxsgit/p2pget/internal/fsutil"
	"github.com/qiuxsgit/p2pget/internal/link"
)

var defaultEDServers = []string{"176.123.5.89:4725", "176.125.242.230:5555", "176.116.0.51:6555", "91.208.162.87:4232"}

type ed2kJob struct {
	client             *goed2k.Client
	handle             goed2k.TransferHandle
	req                link.Request
	path               string
	cancel             context.CancelFunc
	workers            sync.WaitGroup
	actions            chan func()
	lastTick, lastSave time.Time
}

// Each task stores only its own transfer and never restores unrelated shared directories.
type taskEDStore struct {
	ctx                    context.Context
	path, target, identity string
	req                    link.Request
	discovery              bool
}

func (s *taskEDStore) Load() (*goed2k.ClientState, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var state goed2k.ClientState
	if err = json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	for _, transfer := range state.Transfers {
		if !transfer.Hash.Equal(s.req.EDLink.Hash) || transfer.Size != s.req.Size {
			return nil, fmt.Errorf("保存的 ED2K 状态与链接不符")
		}
	}
	if len(state.Transfers) > 1 {
		return nil, fmt.Errorf("状态文件包含多个任务")
	}
	for i := range state.Transfers {
		state.Transfers[i].TargetPath = s.target
		state.Transfers[i].Paused = false
	}
	state.SharedDirs = nil
	state.SharedFiles = nil
	state.IdentityKeyPath = s.identity
	if !s.discovery {
		state.DHT = nil
		state.DHTv6 = nil
	}
	if len(state.Transfers) == 1 {
		if err := checkResumePieces(s.ctx, &state.Transfers[0], s.target, s.req); err != nil {
			return nil, err
		}
	}
	return &state, nil
}
func (s *taskEDStore) Save(state *goed2k.ClientState) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(s.path, raw)
}

func newED2KJob(ctx context.Context, req link.Request, opts engine.Options, taskDir, stateDir string) (*ed2kJob, error) {
	jobCtx, cancel := context.WithCancel(ctx)
	initialized := false
	defer func() {
		if !initialized {
			cancel()
		}
	}()
	settings := goed2k.NewSettings()
	dialer := net.Dialer{Timeout: 3 * time.Second}
	settings.DialContext = func(_ context.Context, network, address string) (net.Conn, error) {
		return dialer.DialContext(jobCtx, network, address)
	}
	settings.Logger = opts.Logger
	settings.ClientName = "P2PGet"
	settings.ModName = "P2PGet"
	settings.ListenPort = 0
	settings.UDPPort = 0
	settings.SessionConnectionsLimit = opts.Connections
	settings.MaxPeerListSize = max(3000, opts.Connections*4)
	settings.MaxConnectionsPerSecond = min(8, opts.Connections)
	settings.PeerConnectionTimeout = 15
	settings.MaxUploadRateKB = opts.UploadKiB
	settings.MaxDownloadRateKB = 0
	settings.ReconnectToServer = !opts.NoDiscovery
	settings.EnableUPnP = false
	settings.EnableDHT = !opts.NoDiscovery
	settings.EnableSecIdent = true
	settings.IdentityKeyPath = filepath.Join(stateDir, "identity.pem")
	settings.PreallocateDiskSpace = false
	settings.UseSparseFiles = true
	settings.EnableWebDownload = false
	target := filepath.Join(taskDir, req.Name)
	// Refuse an existing symlink instead of writing through it.
	if stat, err := os.Lstat(target); err == nil && !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("目标不是普通文件：%s", target)
	}
	client := goed2k.NewClient(settings)
	store := &taskEDStore{ctx: ctx, path: filepath.Join(stateDir, "ed2k.json"), target: target, identity: settings.IdentityKeyPath, req: req, discovery: !opts.NoDiscovery}
	client.SetStateStore(store)
	client.SetAutoSaveInterval(15 * time.Second)
	if err := client.LoadState(""); err != nil {
		return nil, fmt.Errorf("恢复 ED2K 状态失败：%w", err)
	}
	if err := startEDSession(client, !opts.NoDiscovery); err != nil {
		client.Close()
		return nil, err
	}
	handle := client.FindTransfer(req.EDLink.Hash)
	if !handle.IsValid() {
		atp := goed2k.NewAddTransferParamsFromHandler(req.EDLink.Hash, goed2k.CurrentTimeMillis(), req.Size, disk.NewDesktopFileHandler(target), false)
		atp.AICHRootHash = req.EDLink.AICHRootHash
		atp.PieceHashes = req.EDLink.PartHashes
		var err error
		handle, err = client.AddTransfer(atp)
		if err != nil {
			client.Close()
			return nil, err
		}
	} else {
		handle.Resume()
	}
	job := &ed2kJob{client: client, handle: handle, req: req, path: target, cancel: cancel, actions: make(chan func(), 32), lastTick: time.Now(), lastSave: time.Now()}
	for _, address := range opts.Peers {
		addr, err := net.ResolveTCPAddr("tcp", address)
		if err != nil {
			job.Close()
			return nil, err
		}
		transfer := client.Session().LookupTransfer(req.EDLink.Hash)
		if err = transfer.AddPeer(protocol.EndpointFromInet(addr), int(goed2k.PeerServer)); err != nil {
			job.Close()
			return nil, err
		}
	}
	if !opts.NoDiscovery {
		servers := []string(opts.Servers)
		if len(servers) == 0 {
			servers = defaultEDServers
		}
		// Preflight with a cancellable timeout: one offline server must not hold up initial source discovery.
		for _, address := range servers {
			job.workers.Add(1)
			go func(address string) {
				defer job.workers.Done()
				dialer := net.Dialer{Timeout: 3 * time.Second}
				conn, err := dialer.DialContext(jobCtx, "tcp", address)
				if err != nil {
					return
				}
				conn.Close()
				select {
				case job.actions <- func() {
					if err := client.Connect(address); err != nil && opts.Logger != nil {
						opts.Logger.Debug("ED2K server unavailable", "server", address, "error", err)
					}
				}:
				case <-jobCtx.Done():
				}
			}(address)
		}
		job.workers.Add(1)
		go func() {
			defer job.workers.Done()
			bootstrapKad(jobCtx, client, opts.KadNodes, stateDir, opts.Logger, job.actions)
		}()
	}
	initialized = true
	return job, nil
}

func bootstrapKad(ctx context.Context, client *goed2k.Client, source, stateDir string, logger *slog.Logger, actions chan func()) {
	apply := func(path string) error {
		reply := make(chan error, 1)
		select {
		case actions <- func() { reply <- client.LoadDHTNodesDat(path) }:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case err := <-reply:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	cache := filepath.Join(stateDir, "nodes.dat")
	if source == "" {
		if _, err := os.Stat(cache); err == nil {
			if apply(cache) == nil {
				return
			}
		}
		source = "https://www.nodes-dat.com/dl.php?load=nodes,https://upd.emule-security.org/nodes.dat"
	}
	for _, item := range strings.Split(source, ",") {
		if ctx.Err() != nil {
			return
		}
		item = strings.TrimSpace(item)
		parsed, err := url.Parse(item)
		if err != nil {
			continue
		}
		path := item
		if parsed.Scheme == "https" {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, item, nil)
			if err != nil {
				continue
			}
			response, err := (&http.Client{Timeout: 8 * time.Second}).Do(request)
			if err != nil {
				continue
			}
			raw, readErr := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
			response.Body.Close()
			if readErr != nil || response.StatusCode != http.StatusOK || len(raw) > 2<<20 {
				continue
			}
			if err = fsutil.AtomicWrite(cache, raw); err != nil {
				continue
			}
			path = cache
		} else if parsed.Scheme != "" {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if err = apply(path); err == nil {
			return
		}
	}
	if logger != nil && ctx.Err() == nil {
		logger.Warn("Kad 引导节点暂不可用；继续使用 ED2K 服务器找源，可通过 --kad-nodes 指定 nodes.dat")
	}
}

func (j *ed2kJob) Progress() (engine.Progress, error) {
drain:
	for n := 0; n < 32; n++ {
		select {
		case action := <-j.actions:
			action()
		default:
			break drain
		}
	}
	now := time.Now()
	goed2k.UpdateCachedTime()
	j.client.Session().SecondTick(goed2k.CurrentTime(), max(1, now.Sub(j.lastTick).Milliseconds()))
	j.lastTick = now
	if now.Sub(j.lastSave) >= 15*time.Second {
		if err := j.client.SaveState(""); err != nil {
			return engine.Progress{}, err
		}
		j.lastSave = now
	}
	status := j.handle.GetStatus()
	if status.Paused {
		return engine.Progress{}, fmt.Errorf("ED2K 任务异常暂停，请检查剩余磁盘空间和目录写入权限")
	}
	p := engine.Progress{Phase: "寻找 / 排队", Done: status.TotalDone, Total: j.req.Size, Received: status.TotalReceived, Peers: j.handle.ActiveConnections(), Known: status.NumPeers, Rate: int64(status.DownloadRate)}
	if status.DownloadRate > 0 {
		p.Phase = "下载中"
	}
	if status.State == goed2k.Verifying {
		p.Phase = "校验中"
	}
	if j.handle.IsFinished() {
		p.Phase = "已完成"
	}
	for _, server := range j.client.ServerStatuses() {
		if server.HandshakeCompleted {
			p.Servers++
		}
	}
	p.Kad = j.client.DHTStatus().KnownNodes
	return p, nil
}
func (j *ed2kJob) Verify(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	hash, size, _, err := hashED2KFile(ctx, j.path)
	if err != nil {
		return err
	}
	if size != j.req.Size || !hash.Equal(j.req.EDLink.Hash) {
		return fmt.Errorf("ED2K 文件的最终哈希或大小不符，不能视为下载成功")
	}
	return nil
}
func (j *ed2kJob) Close() error {
	j.cancel()
	j.workers.Wait()
	j.handle.Pause()
	j.client.Session().FlushDiskTasks()
	var syncErr error
	if file := j.handle.GetFile(); file != nil {
		syncErr = file.Sync()
	}
	saveErr := j.client.SaveState("")
	stopErr := j.client.Stop()
	if file := j.handle.GetFile(); file != nil {
		_ = file.Close()
	}
	return errors.Join(syncErr, saveErr, stopErr)
}

// The adapter owns the session loop. The upstream Client.Start status goroutine is intentionally unused.
func startEDSession(client *goed2k.Client, discovery bool) error {
	if discovery && client.GetDHTTracker() == nil {
		client.EnableDHT()
	}
	session := client.Session()
	if err := session.Listen(); err != nil {
		return err
	}
	if err := session.EnsureServerStatUDPListener(); err != nil {
		return err
	}
	if tracker := client.GetDHTTracker(); tracker != nil {
		if err := tracker.Start(); err != nil {
			return err
		}
		session.SyncDHTListenPort()
	}
	return nil
}

func hashED2KFile(ctx context.Context, path string) (protocol.Hash, int64, []protocol.Hash, error) {
	file, err := os.Open(path)
	if err != nil {
		return protocol.Invalid, 0, nil, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return protocol.Invalid, 0, nil, err
	}
	size := stat.Size()
	buf := make([]byte, goed2k.PieceSize)
	hashes := []protocol.Hash{}
	for left := size; left > 0; {
		if err := ctx.Err(); err != nil {
			return protocol.Invalid, size, nil, err
		}
		n := min(int64(len(buf)), left)
		if _, err = io.ReadFull(file, buf[:n]); err != nil {
			return protocol.Invalid, size, nil, err
		}
		hash, err := protocol.HashFromData(buf[:n])
		if err != nil {
			return protocol.Invalid, size, nil, err
		}
		hashes = append(hashes, hash)
		left -= n
	}
	return protocol.HashFromHashSet(hashes), size, hashes, nil
}

func checkResumePieces(ctx context.Context, transfer *goed2k.ClientTransferState, target string, req link.Request) error {
	if ctx == nil {
		ctx = context.Background()
	}
	rd := transfer.ResumeData
	if rd == nil {
		return nil
	}
	count := int((req.Size + goed2k.PieceSize - 1) / goed2k.PieceSize)
	hashes := rd.Hashes
	if count == 1 && len(hashes) == 0 {
		hashes = []protocol.Hash{req.EDLink.Hash}
	}
	if len(hashes) != count || !protocol.HashFromHashSet(hashes).Equal(req.EDLink.Hash) {
		transfer.ResumeData = nil
		return nil
	}
	// Incomplete pieces are downloaded again; only complete, freshly hashed pieces are trusted.
	bits := protocol.NewBitField(count)
	file, err := os.Open(target)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		defer file.Close()
		buf := make([]byte, goed2k.PieceSize)
		for i := 0; i < count; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if i >= rd.Pieces.Len() || !rd.Pieces.GetBit(i) {
				continue
			}
			n := min(goed2k.PieceSize, req.Size-int64(i)*goed2k.PieceSize)
			if _, err := file.ReadAt(buf[:n], int64(i)*goed2k.PieceSize); err != nil {
				continue
			}
			hash, _ := protocol.HashFromData(buf[:n])
			if hash.Equal(hashes[i]) {
				bits.SetBit(i)
			}
		}
	}
	rd.Pieces = bits
	rd.DownloadedBlocks = nil
	return nil
}
