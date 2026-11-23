// Package download runs one BT or ED2K task to completion and verifies it.
package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/qiuxsgit/p2pget/internal/engine"
	"github.com/qiuxsgit/p2pget/internal/engine/bt"
	"github.com/qiuxsgit/p2pget/internal/engine/ed2k"
	"github.com/qiuxsgit/p2pget/internal/fsutil"
	"github.com/qiuxsgit/p2pget/internal/link"
	"github.com/qiuxsgit/p2pget/internal/progress"
)

// Run downloads req into its task directory under opts.Dir, resuming saved state.
func Run(ctx context.Context, req link.Request, opts engine.Options, out io.Writer) (retErr error) {
	taskDir := filepath.Join(opts.Dir, req.Kind+"-"+req.ID)
	stateDir := filepath.Join(taskDir, ".p2pget")
	release, err := acquireTaskLock(stateDir)
	if err != nil {
		return err
	}
	defer release()
	if err = fsutil.AtomicWrite(filepath.Join(stateDir, "link.txt"), []byte(req.Raw+"\n")); err != nil {
		return err
	}
	fmt.Fprintf(out, "协议：%s  |  连接上限：%d  |  下载不限速\n", strings.ToUpper(req.Kind), opts.Connections)
	fmt.Fprintln(out, "保存目录：", taskDir)
	fmt.Fprintln(out, "按 Ctrl+C 保存进度并退出；再次输入同一链接续传。")
	if opts.UploadKiB > 0 {
		fmt.Fprintf(out, "下载期间会上传已获得的分块，上限 %d KiB/s；完成后停止。\n", opts.UploadKiB)
	} else {
		fmt.Fprintln(out, "下载期间上传不限速；完成后停止。")
	}
	printer := progress.NewPrinter(out)
	defer printer.Finish()
	if opts.Logger != nil {
		opts.Logger = slog.New(progress.NewLogHandler(printer, opts.Logger.Handler()))
		previousLogger := slog.Default()
		slog.SetDefault(opts.Logger)
		defer slog.SetDefault(previousLogger)
	}
	var job engine.Job
	if req.Kind == "bt" {
		job, err = bt.New(req, opts, taskDir, stateDir)
	} else {
		job, err = ed2k.New(ctx, req, opts, taskDir, stateDir)
	}
	if err != nil {
		return err
	}
	defer func() {
		if job != nil {
			retErr = errors.Join(retErr, job.Close())
		}
	}()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	meter := progress.SpeedMeter{}
	lastPaint := time.Time{}
	for {
		p, err := job.Progress()
		if err != nil {
			return err
		}
		now := time.Now()
		p.Rate = meter.Observe(now, p.Received)
		if time.Since(lastPaint) >= time.Second || p.Phase == "已完成" {
			lastPaint = time.Now()
			printer.Render(p)
		}
		if p.Phase == "已完成" {
			printer.Finish()
			fmt.Fprintln(out, "正在确认文件校验结果…")
			if err = job.Verify(ctx); err != nil {
				return err
			}
			if err = job.Close(); err != nil {
				job = nil
				return err
			}
			job = nil
			fmt.Fprintln(out, "下载完成，校验通过。文件位于：", taskDir)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
