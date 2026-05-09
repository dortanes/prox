// Package watcher monitors a file for changes using stat-based polling.
package watcher

import (
	"context"
	"log/slog"
	"os"
	"time"
)

const pollInterval = 1 * time.Second

// Watch monitors a file for modifications and calls onChange when the file
// changes. It compares mtime + size on each tick. Blocks until ctx is cancelled.
func Watch(ctx context.Context, path string, onChange func()) {
	prev, err := snapshot(path)
	if err != nil {
		slog.Warn("file watcher: cannot stat file, watching disabled",
			"path", path,
			"error", err,
		)
		return
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur, err := snapshot(path)
			if err != nil {
				slog.Debug("file watcher: stat error", "error", err)
				continue
			}

			if cur != prev {
				slog.Info("config file changed, triggering reload", "path", path)
				onChange()
				prev = cur
			}
		}
	}
}

// fileSnapshot captures the relevant metadata for change detection.
type fileSnapshot struct {
	modTime time.Time
	size    int64
}

func snapshot(path string) (fileSnapshot, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileSnapshot{}, err
	}
	return fileSnapshot{
		modTime: info.ModTime(),
		size:    info.Size(),
	}, nil
}
