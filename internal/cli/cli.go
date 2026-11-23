// Package cli parses p2pget command-line arguments and drives a download.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/qiuxsgit/p2pget/internal/download"
	"github.com/qiuxsgit/p2pget/internal/engine"
	"github.com/qiuxsgit/p2pget/internal/link"
)

const version = "0.1.1"

type stringFlags []string

func (s *stringFlags) String() string         { return strings.Join(*s, ",") }
func (s *stringFlags) Set(value string) error { *s = append(*s, value); return nil }

// Run executes p2pget with args and returns the process exit code.
func Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("p2pget", flag.ContinueOnError)
	flags.SetOutput(errOut)
	opts := engine.Options{}
	flags.StringVar(&opts.Dir, "dir", "", "下载根目录，默认是当前目录下的 downloads")
	flags.StringVar(&opts.Dir, "o", "", "-dir 的简写")
	flags.IntVar(&opts.Connections, "connections", 300, "最大节点连接数（1–2000）")
	flags.IntVar(&opts.UploadKiB, "upload", 256, "上传上限 KiB/s，0 表示不限速")
	flags.DurationVar(&opts.Timeout, "timeout", 0, "本次最多运行时间，例如 10m；0 表示不限时")
	flags.BoolVar(&opts.NoDiscovery, "no-discovery", false, "关闭自动找源，仅使用手动指定的节点")
	flags.Var((*stringFlags)(&opts.Servers), "server", "ED2K 服务器 host:port，可重复指定")
	flags.Var((*stringFlags)(&opts.Peers), "peer", "直接连接已知文件节点 host:port，可重复指定")
	flags.Var((*stringFlags)(&opts.Trackers), "tracker", "附加 BT Tracker 地址，可重复指定")
	flags.StringVar(&opts.KadNodes, "kad-nodes", "", "ED2K Kad nodes.dat 文件路径或 HTTPS 地址")
	showVersion := flags.Bool("version", false, "显示版本")
	verbose := flags.Bool("verbose", false, "显示内核诊断信息")
	flags.Usage = func() {
		fmt.Fprint(errOut, "P2PGet "+version+" — 免安装 BT / ED2K 下载器\n用法：p2pget [选项] \"链接或种子文件\"\n不传链接时会提示粘贴。参数须放在链接前面。\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(out, "P2PGet "+version)
		return 0
	}
	if opts.Connections < 1 || opts.Connections > 2000 || opts.UploadKiB < 0 || opts.UploadKiB > 1024*1024 || opts.Timeout < 0 {
		fmt.Fprintln(errOut, "连接数应在 1–2000 之间；上传限速和时限不能为负数。")
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(errOut, "每次接受一个链接；请用引号包住完整链接。")
		return 2
	}
	interactive := flags.NArg() == 0
	reader := bufio.NewReader(in)
	value := flags.Arg(0)
	if interactive {
		fmt.Fprintln(out, "P2PGet "+version+" — BT / ED2K 下载器")
		fmt.Fprint(out, "粘贴磁力链接、ED2K 链接或 .torrent 路径，然后回车：\n> ")
		line, err := readLineContext(ctx, reader)
		if err != nil && len(line) == 0 {
			if ctx.Err() != nil {
				return 130
			}
			fmt.Fprintln(errOut, "没有收到下载链接。")
			return 2
		}
		value = line
	}
	req, err := link.Parse(value)
	if err != nil {
		fmt.Fprintln(errOut, "错误：", err)
		return 2
	}
	if opts.Dir == "" {
		opts.Dir, err = defaultDownloadDir()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	}
	opts.Dir, err = filepath.Abs(opts.Dir)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	level := slog.LevelError
	if *verbose {
		level = slog.LevelDebug
	}
	opts.Logger = slog.New(slog.NewTextHandler(errOut, &slog.HandlerOptions{Level: level}))
	// Both engines accept the same logger. No application/service is installed.
	slog.SetDefault(opts.Logger)
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	err = download.Run(ctx, req, opts, out)
	code := 0
	if err != nil {
		code = 1
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintln(out, "\n已停止，进度已保存。用同一链接和目录再次启动即可续传。")
			code = 130
		} else {
			fmt.Fprintln(errOut, "\n下载失败：", err)
		}
	}
	if interactive && ctx.Err() == nil {
		fmt.Fprint(out, "\n按回车退出…")
		_, _ = readLineContext(ctx, reader)
	}
	return code
}

func readLineContext(ctx context.Context, in io.Reader) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		reader, ok := in.(*bufio.Reader)
		if !ok {
			reader = bufio.NewReader(in)
		}
		line, err := reader.ReadString('\n')
		ch <- result{line, err}
	}()
	select {
	case r := <-ch:
		return r.line, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
