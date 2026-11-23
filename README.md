# P2PGet

Go 编写的命令行 BT / ED2K 下载工具，支持磁力链接、种子文件、断点续传和单行进度显示。支持 macOS、Linux 和 Windows。当前版本：**0.1.1**，需要 **Go 1.26 或更新版本**。

## 从源码开始

先克隆仓库并进入源码目录：

```sh
gh repo clone qiuxsgit/p2pget
cd p2pget
```

没有 GitHub CLI 时，可使用 `git clone https://github.com/qiuxsgit/p2pget.git`。然后运行：

```sh
go run . "完整下载地址"
```

也可以不传地址，启动后粘贴：

```sh
go run .
```

指定下载目录或节点上限：

```sh
go run . --dir ./downloads --connections 600 "magnet:?xt=urn:btih:完整哈希"
go run . --dir ./downloads "ed2k://|file|文件名|字节数|文件哈希|/"
go run . --dir ./downloads ./example.torrent
```

所有参数放在地址之前，完整链接用引号包住。项目包含多个 `.go` 文件，所以请用 `go run .`。
首次运行时 Go 会下载 `go.mod` 中固定的依赖；后续会使用本机缓存。如果本机没有 C 编译环境，可使用 `CGO_ENABLED=0 go run . "完整下载地址"`。

终端里进度按窗口宽度自动缩短、原地刷新。写入日志文件时，每次进度更新单独成行，不包含光标控制字符：

```sh
go run . "完整下载地址" > download.log 2>&1
```

速度显示采用最近约三秒的数据平均值，减少网络分块到达时速度在零和瞬间峰值之间跳动。诊断信息会先结束当前进度行，再输出到新行。

## 文件、续传和连接

默认保存在当前工作目录下的 `downloads`（不是 `go run` 的临时编译目录），也可通过 `--dir` / `-o` 选择目录。每个任务使用一个包含协议和文件哈希的子目录，以免相同文件名相互覆盖；该目录中的 `.p2pget` 保存链接和恢复信息。

按 Ctrl+C 保存并退出；下一次输入相同链接并选择相同的下载根目录即可续传。BT 会重新检查已有数据，ED2K 会重新检查已完成的分片。ED2K 未完成的单个分片重新下载，因此中断后进度可能回退少量。不要删除恢复目录。

默认节点连接上限 300、下载不限速。可按需要提高，例如：

```sh
go run . --connections 600 --upload 512 --dir ./downloads "完整链接"
```

`--upload` 单位 KiB/s，默认 256，0 表示不限速。下载过程中会上传已获得的分块，任务完成后退出。节点数是上限，实际速度取决于在线来源、对方带宽、排队及网络可达性，提高上限不保证提速。

每次处理一个任务，可以在不同终端运行不同任务。同一任务目录有进程锁，避免两个实例同时写入。即使 BT 和 ED2K 链接看起来是同一个文件，也分别下载和校验，不会把两种协议写入同一个文件。

## ED2K 来源

内置多个 ED2K 服务器地址，支持 Kad 和来源交换。首次可能下载用于找源的 `nodes.dat`（节点列表，不是额外程序），保存在任务恢复目录。来源不足或 Kad 引导不可用时，会继续通过可用的 ED2K 服务器找源。

常用选项：

```sh
go run . --server "服务器地址:端口" --kad-nodes ./nodes.dat "ed2k://..."
go run . --peer "已知节点:端口" --no-discovery "完整链接"
go run . --timeout 10m "完整链接"
go run . --verbose "完整链接"
go run . --help
go run . --version
```

`--server`、`--peer`、`--tracker` 可重复使用。`--no-discovery` 关闭自动找源，只用于有已知直连节点的情况。`--timeout` 到时保存进度退出，默认不限时。服务器和引导节点可能离线，可自行指定新的地址。网络限制、低 ID 或排队会影响 ED2K 的速度和可用性。工具不修改路由器或系统网络配置。

## 验证范围

已在 macOS Apple Silicon 上检查源码运行和终端显示，并通过本地 BT/ED2K 传输、续传测试。公网是否有可用来源取决于具体资源与网络；Windows 和 Linux 的实机验证范围见 [VERIFICATION.md](VERIFICATION.md)。

## 测试及可选本机构建

需要 Go 1.26 或更新版本：

```sh
go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o p2pget .
```

PowerShell 可先设置 `$env:CGO_ENABLED="0"`，再运行 `go build -trimpath -ldflags="-s -w" -o p2pget.exe .`。

源码包含 `third_party/goed2k` 的必要修订，详见 `PATCHES.md`；其余依赖由 `go.mod` 和 `go.sum` 固定。第三方版权和许可见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) 与 `THIRD_PARTY_LICENSES`。BT 内核的对应源码可从 https://github.com/anacrolix/torrent/tree/v1.63.0 取得；内嵌 ED2K 修订源码随源码包提供。

## 许可证

P2PGet 采用 [GNU GPL v3.0](LICENSE) 许可证。第三方依赖保留各自的版权及许可，内嵌 ED2K 修改记录见 [PATCHES.md](PATCHES.md)。

## 反馈与贡献

欢迎通过 [Issues](https://github.com/qiuxsgit/p2pget/issues) 报告问题或提交 Pull Request。提交问题时请附上版本、系统、运行参数和相关日志；请先删除日志中的个人路径及敏感信息。
