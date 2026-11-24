// Package engine defines the types shared by the BT and ED2K download engines.
package engine

import (
	"context"
	"log/slog"
	"time"
)

type Options struct {
	Dir                      string
	Connections              int
	UploadKiB                int
	Timeout                  time.Duration
	NoDiscovery              bool
	Servers, Peers, Trackers []string
	KadNodes                 string
	Logger                   *slog.Logger
}

type Progress struct {
	Phase                      string
	Done, Total                int64
	Received                   int64
	Peers, Known, Servers, Kad int
	Rate                       int64
}

type Job interface {
	Progress() (Progress, error)
	Verify(context.Context) error
	// Path is the downloaded file, or the directory of a multi-file torrent; it may be
	// empty until the engine knows the file names.
	Path() string
	Close() error
}
