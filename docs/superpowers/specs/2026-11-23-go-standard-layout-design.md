# P2PGet 按 Go 标准结构重组：设计

## 目的

现在所有源文件都在仓库根目录，统一是 `package main`。按 Go 常见的 `cmd/` + `internal/` 结构拆开，让 BT、ED2K、链接解析、进度显示各自有清晰的包边界，以后加新协议或别的入口时不必再动整个 `main` 包。

## 目标

- 入口移到 `cmd/p2pget/`，业务代码拆进 `internal/` 下的独立包。
- BT 和 ED2K 引擎各自一个包。
- 保留全部现有测试，测试内容不削弱。

## 非目标

- 不改程序行为：命令行参数、输出文字、退出码、下载目录和 `.p2pget` 状态文件格式全部保持不变。
- 不动 `third_party/goed2k` 和 `go.mod` 的依赖。
- 不顺带重构逻辑（例如把阶段字符串 "已完成" 改成常量），只做搬移和必要的导出。

## 反目标

- 包之间出现循环引用，或为绕开循环而复制同一段逻辑。
- 为了让测试能编译而删掉或放宽断言。

## 方案对比

1. **只加 `cmd/p2pget/`**：文件原样移动，不拆包。改动最小，但没有包边界。
2. **引擎和主循环同包**：5 个包，测试只换目录。BT 和 ED2K 仍混在一个包里。
3. **BT、ED2K 各自成包（已选）**：7 个包。需要一个公共的 `engine` 包放共享类型，`AtomicWrite` 单独成包，少量函数要导出给测试用。协议之间完全独立，后续扩展成本最低。

选择方案 3 的原因：用户希望每种协议的边界清晰。

## 包结构与依赖

```
cmd/p2pget/          main()
internal/cli/        → download, engine, link, progress
internal/download/   → engine, engine/bt, engine/ed2k, fsutil, link, progress
internal/engine/bt/  → engine, fsutil, link
internal/engine/ed2k/→ engine, fsutil, link
internal/progress/   → engine
internal/engine/     （叶子，不依赖其他内部包）
internal/link/       （叶子）
internal/fsutil/     （叶子）
```

`engine.Progress` 放在 `engine` 包而不是 `progress` 包：它是引擎上报的数据，进度条只负责显示。这样 `progress` 依赖 `engine`，引擎不依赖显示层。

## 代码去向

| 现在 | 新位置 | 导出名（跨包使用的才导出） |
|------|--------|------|
| `main()`（main.go） | `cmd/p2pget/main.go` | 调用 `progress.ConfigureConsole()` 和 `cli.Run(...)` |
| `cli`、`version`、`stringFlags`、`readLineContext`（main.go） | `internal/cli` | `Run` |
| `defaultDownloadDir`（progress.go） | `internal/cli` | 不导出 |
| `download`（main.go） | `internal/download` | `Run(ctx, req, opts, out)` |
| `acquireTaskLock`（state.go） | `internal/download` | 不导出 |
| `atomicWrite`（state.go） | `internal/fsutil` | `AtomicWrite` |
| `options`、`progress`、`downloadJob`（main.go） | `internal/engine` | `Options`、`Progress`、`Job` |
| `request`、`parseInput`、`btRequest`、`validateFileName`、`validateTorrentInfo`（input.go） | `internal/link` | `Request`、`Parse`、`FromTorrentSpec`、`ValidateFileName`、`ValidateTorrentInfo` |
| bt.go 全部 | `internal/engine/bt` | `New`、`Job` |
| ed2k.go 全部 | `internal/engine/ed2k` | `New`、`Job`、`StartSession` |
| progress.go 其余部分 | `internal/progress` | `Printer`、`NewPrinter`、`NewLogHandler`、`SpeedMeter` |
| console_windows.go / console_other.go | `internal/progress` | `ConfigureConsole`；`enableProgressANSI` 不导出 |

细节：

- `Options` 里的 `Servers`、`Peers`、`Trackers` 改为普通 `[]string`。`cli` 保留自己的 `stringFlags` 类型，注册参数时用 `(*stringFlags)(&opts.Servers)` 转换。
- `download` 现在直接构造 `progressLogHandler{...}`，改为调用 `progress.NewLogHandler(printer, handler)`。
- `ed2k.StartSession` 导出，是因为 `download` 包的端到端测试要用它启动做种端。

## 测试去向

| 测试 | 新位置 | 说明 |
|------|--------|------|
| `TestParseValidLinks`、`TestRejectInvalidAndUnsafeLinks`、`TestPortableFileNames`、`TestRejectTorrentCaseAndStatePathCollisions` | `internal/link` | 直接搬 |
| `TestTaskLockReleasesWithoutDeletingDownloads` | `internal/download` | 直接搬 |
| `TestBTPartialDownloadAndRestart`、`TestED2KRealPeerTransferAndRestart` | `internal/download` | 调用 `bt.New`、`ed2k.StartSession`、`Run`，只用导出接口 |
| `TestED2KPartialResumeWhileUploading`、`TestED2KResumeDoesNotTrustChangedOrMissingFiles` | `internal/engine/ed2k` | 包内测试，可继续读 `job.client`、`job.handle`、`taskEDStore` |
| `TestInputPromptCanBeCancelled`、`TestDefaultDownloadDirSurvivesGoRun` | `internal/cli` | 直接搬 |
| 其余进度测试 | `internal/progress` | 包内测试，可继续读 `Printer` 未导出字段 |

`testOptions` 辅助函数在 `download` 和 `ed2k` 两个测试包里各保留一份，避免为测试单独建包。

## 文档

- README：所有 `go run .` 改为 `go run ./cmd/p2pget`，构建命令改为 `go build ... -o p2pget ./cmd/p2pget`，删掉"项目包含多个 `.go` 文件，所以请用 `go run .`"这句的旧解释。
- CHANGELOG：新增一条"源码改为 `cmd/` + `internal/` 结构，入口为 `./cmd/p2pget`"。
- `VERIFICATION.md` 是 0.1.1 的历史记录，不改。

## 验收

- `CGO_ENABLED=0 go test ./... -count=1` 全部通过（`third_party/goed2k` 有自己的 `go.mod`，不在 `./...` 内）。
- `go vet ./...` 通过。
- `GOOS=windows go build ./cmd/p2pget` 能编译，确认 Windows 控制台代码没断。
- `go run ./cmd/p2pget --version` 输出 `P2PGet 0.1.1`。
- 用 `git mv` 移动文件，保留历史。

## 留给实现者

- 每个包内部的文件名（例如 `ed2k` 包是否把 `taskEDStore` 拆成 `store.go`）。
- 包注释的措辞。
