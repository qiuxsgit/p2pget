# Go 标准结构重组 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把根目录的 `package main` 拆成 `cmd/p2pget` + `internal/` 下 8 个包，行为不变。

**Architecture:** 从叶子包开始逐个搬出（fsutil、link、engine → progress → bt、ed2k → download、cli、cmd）。每个任务结束时根目录剩余的 `package main` 改为 import 新包，整个仓库保持可编译、测试全绿。文件用 `git mv` 移动以保留历史。

**Tech Stack:** Go 1.26（go.mod），anacrolix/torrent，goed2k（`third_party/goed2k` 本地 replace）。

**Spec:** `docs/superpowers/specs/2026-11-23-go-standard-layout-design.md`

## Global Constraints

- 命令行参数、输出文字、退出码、下载目录和 `.p2pget` 状态文件格式不变。
- 不改 `third_party/goed2k`、`go.mod` 依赖。
- 只做搬移和必要的导出，不重构逻辑；测试断言不删不放宽。
- 包依赖必须符合 spec 的依赖图；`engine`、`link`、`fsutil` 不依赖其他内部包。
- 模块路径 `github.com/qiuxsgit/p2pget`，内部包路径形如 `github.com/qiuxsgit/p2pget/internal/link`。
- 每个任务结束时：`CGO_ENABLED=0 go test ./... -count=1` 和 `go vet ./...` 通过。

## Review Focus

1. 重复的 `--server` / `--peer` / `--tracker` 参数改成 `[]string` 后仍然逐个累加 → Task 5 加 `TestRepeatedFlagsAccumulate`。
2. Windows 控制台代码（`console_windows.go`）只在 Windows 编译，本机测试覆盖不到 → 每个动到 progress 或 cmd 的任务跑 `GOOS=windows go build ./...`。
3. `--version` 输出和退出码 → Task 5 加 `TestRunVersion`（输出 `P2PGet 0.1.1\n`，返回 0）。
4. 传两个链接时退出码 2 → Task 5 加 `TestRunRejectsTwoLinks`。
5. 引擎日志仍经过进度条的 log handler（不覆盖进度行）→ 由已有 `TestProgressDiagnosticsStartOnNewLine` 覆盖，`download.Run` 必须调用 `progress.NewLogHandler`。

---

### Task 1: 叶子包 fsutil、link、engine

**Files:**
- Create: `internal/fsutil/atomic.go`（从 `state.go` 搬 `atomicWrite`）
- Move: `input.go` → `internal/link/link.go`，`input_test.go` → `internal/link/link_test.go`
- Create: `internal/engine/engine.go`（从 `main.go` 搬 `options`、`progress`、`downloadJob`）
- Modify: 根目录所有引用这些名字的文件；`TestTaskLockReleasesWithoutDeletingDownloads` 暂时移到根目录 `state_test.go`

**Interfaces:**
- Produces:
  - `fsutil.AtomicWrite(path string, data []byte) error`
  - `link.Request{Kind, ID, Name, Raw string; Size int64; Spec *torrent.TorrentSpec; EDLink goed2k.EMuleLink}`
  - `link.Parse(value string) (link.Request, error)`
  - `link.FromTorrentSpec(spec *torrent.TorrentSpec, raw string) (link.Request, error)`（原 `btRequest`）
  - `link.ValidateFileName(name string) error`，`link.ValidateTorrentInfo(info *metainfo.Info) error`
  - `engine.Options{Dir string; Connections, UploadKiB int; Timeout time.Duration; NoDiscovery bool; Servers, Peers, Trackers []string; KadNodes string; Logger *slog.Logger}`
  - `engine.Progress{Phase string; Done, Total, Received int64; Peers, Known, Servers, Kad int; Rate int64}`
  - `engine.Job interface{ Progress() (engine.Progress, error); Verify(context.Context) error; Close() error }`

- [ ] **Step 1:** `git mv input.go internal/link/link.go`、`git mv input_test.go internal/link/link_test.go`，包名改 `link`，按上面导出改名；把 `TestTaskLockReleasesWithoutDeletingDownloads` 剪到根目录 `state_test.go`。
- [ ] **Step 2:** 建 `internal/fsutil/atomic.go`、`internal/engine/engine.go`，从根目录删掉对应定义。`main.go` 保留 `stringFlags`，注册参数时用 `(*stringFlags)(&opts.Servers)` 等。
- [ ] **Step 3:** 根目录其余文件改用 `link.`、`engine.`、`fsutil.` 前缀。
- [ ] **Step 4:** Run `CGO_ENABLED=0 go test ./... -count=1 && go vet ./...` — Expected: 全部 PASS。
- [ ] **Step 5:** Commit `refactor: extract link, engine and fsutil packages`。

### Task 2: progress 包

**Files:**
- Move: `progress.go` → `internal/progress/progress.go`，`progress_test.go` → `internal/progress/progress_test.go`
- Move: `console_other.go`、`console_windows.go` → `internal/progress/`
- `defaultDownloadDir` 和 `TestDefaultDownloadDirSurvivesGoRun` 留在根目录（Task 5 进 cli）

**Interfaces:**
- Consumes: `engine.Progress`
- Produces:
  - `progress.Printer`（原 `progressPrinter`，字段保持未导出）
  - `progress.NewPrinter(out io.Writer) *progress.Printer`，方法 `Render(engine.Progress)`、`Finish()`
  - `progress.NewLogHandler(p *progress.Printer, h slog.Handler) slog.Handler`
  - `progress.SpeedMeter`，方法 `Observe(now time.Time, received int64) int64`
  - `progress.ConfigureConsole()`

- [ ] **Step 1:** `git mv` 四个文件，包名改 `progress`，按上面改名；测试里 `sampleProgress()` 返回 `engine.Progress`。
- [ ] **Step 2:** 根目录 `main.go` 改用这些导出名。
- [ ] **Step 3:** Run `CGO_ENABLED=0 go test ./... -count=1 && go vet ./... && GOOS=windows go build ./...` — Expected: 全部通过。
- [ ] **Step 4:** Commit `refactor: extract progress package`。

### Task 3: bt 包

**Files:**
- Move: `bt.go` → `internal/engine/bt/bt.go`

**Interfaces:**
- Consumes: `engine.Options`、`engine.Progress`、`link.Request`、`link.FromTorrentSpec`、`link.ValidateTorrentInfo`、`fsutil.AtomicWrite`
- Produces: `bt.Job`（原 `btJob`，实现 `engine.Job`），`bt.New(req link.Request, opts engine.Options, taskDir, stateDir string) (*bt.Job, error)`

- [ ] **Step 1:** `git mv`，包名改 `bt`，改名并换前缀。
- [ ] **Step 2:** 根目录 `main.go`、`transfer_test.go` 改用 `bt.New`。
- [ ] **Step 3:** Run `CGO_ENABLED=0 go test ./... -count=1 && go vet ./...` — Expected: PASS。
- [ ] **Step 4:** Commit `refactor: extract bt engine package`。

### Task 4: ed2k 包

**Files:**
- Move: `ed2k.go` → `internal/engine/ed2k/ed2k.go`
- Move: `state_test.go` 中的 `TestED2KResumeDoesNotTrustChangedOrMissingFiles` → `internal/engine/ed2k/store_test.go`
- Move: `transfer_test.go` 中的 `TestED2KPartialResumeWhileUploading` → `internal/engine/ed2k/ed2k_test.go`，并在该文件放一份 `testOptions(dir string) engine.Options`

**Interfaces:**
- Consumes: 同 Task 3
- Produces: `ed2k.Job`（原 `ed2kJob`），`ed2k.New(ctx context.Context, req link.Request, opts engine.Options, taskDir, stateDir string) (*ed2k.Job, error)`，`ed2k.StartSession(client *goed2k.Client, discovery bool) error`

- [ ] **Step 1:** `git mv ed2k.go`，包名改 `ed2k`，改名并换前缀。
- [ ] **Step 2:** 搬两个测试，用 `link.Parse` 代替 `parseInput`，其余断言原样。
- [ ] **Step 3:** 根目录 `main.go`、`transfer_test.go` 改用 `ed2k.New`、`ed2k.StartSession`。
- [ ] **Step 4:** Run `CGO_ENABLED=0 go test ./... -count=1 && go vet ./...` — Expected: PASS，`internal/engine/ed2k` 含两个测试。
- [ ] **Step 5:** Commit `refactor: extract ed2k engine package`。

### Task 5: download、cli、cmd，删除根目录 Go 文件

**Files:**
- Create: `internal/download/download.go`（`download` → `Run`；`state.go` 的 `acquireTaskLock`）
- Move: `transfer_test.go` 剩余两个测试 → `internal/download/transfer_test.go`（含 `testOptions`）；`TestTaskLockReleasesWithoutDeletingDownloads` → `internal/download/lock_test.go`
- Create: `internal/cli/cli.go`（`cli` → `Run`、`version`、`stringFlags`、`readLineContext`、`defaultDownloadDir`）
- Move: `TestInputPromptCanBeCancelled`、`TestDefaultDownloadDirSurvivesGoRun` → `internal/cli/cli_test.go`
- Create: `cmd/p2pget/main.go`
- Delete: 根目录剩余的 `main.go`、`state.go`、`state_test.go`、`transfer_test.go`

**Interfaces:**
- Consumes: 前面所有任务的导出
- Produces:
  - `download.Run(ctx context.Context, req link.Request, opts engine.Options, out io.Writer) error`
  - `cli.Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int`
  - `cmd/p2pget/main.go`：`progress.ConfigureConsole()`，信号 context，`os.Exit(cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))`

- [ ] **Step 1: 写新测试** `internal/cli/cli_test.go`：

```go
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
```

- [ ] **Step 2:** 建 download、cli、cmd，`git mv` 测试文件，删根目录 Go 文件。`download.Run` 用 `progress.NewLogHandler` 包装 logger。
- [ ] **Step 3:** Run `CGO_ENABLED=0 go test ./... -count=1 && go vet ./... && GOOS=windows go build ./... && go run ./cmd/p2pget --version` — Expected: 全部通过，最后输出 `P2PGet 0.1.1`。
- [ ] **Step 4:** Run `ls *.go` — Expected: 没有文件。
- [ ] **Step 5:** Commit `refactor: move entry point to cmd/p2pget and add cli/download packages`。

### Task 6: 文档

**Files:**
- Modify: `README.md`（所有 `go run .` → `go run ./cmd/p2pget`；两处构建命令末尾 `.` → `./cmd/p2pget`；第 34 行"项目包含多个 `.go` 文件，所以请用 `go run .`。"改为"入口位于 `cmd/p2pget`。"）
- Modify: `CHANGELOG.md`（顶部新增"未发布"条目：源码改为 `cmd/` + `internal/` 结构，入口为 `./cmd/p2pget`）

- [ ] **Step 1:** 改两个文件。
- [ ] **Step 2:** Run `grep -n "go run \.\|-o p2pget \.\|p2pget.exe \.$" README.md` — Expected: 无输出。
- [ ] **Step 3:** Commit `docs: update commands for cmd/p2pget layout`。
