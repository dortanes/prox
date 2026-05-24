// Package watcher monitors config files for changes using stat-based polling.
package watcher

import (
	"context"
	"log/slog"
	"os"
	"time"
)

const pollInterval = 1 * time.Second

// Watch monitors one or more files for modifications and calls onChange when
// any file changes. It compares mtime + size on each tick. Blocks until ctx is cancelled.
func Watch(ctx context.Context, paths []string, onChange func()) {
	snapshots := make(map[string]fileSnapshot, len(paths))

	for _, p := range paths {
		s, err := snapshot(p)
		if err != nil {
			slog.Warn("watcher: file not accessible",
				"path", p,
				"err", err,
			)
			continue
		}
		snapshots[p] = s
	}

	if len(snapshots) == 0 {
		slog.Warn("watcher: no files to watch")
		return
	}

	slog.Debug("file watcher started", "files", len(snapshots))

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for p, prev := range snapshots {
				cur, err := snapshot(p)
				if err != nil {
					slog.Debug("watcher: stat failed", "path", p, "err", err)
					continue
				}

				if cur != prev {
					slog.Info("config changed", "path", p)
					snapshots[p] = cur
					onChange()
					break // one reload per tick is enough
				}
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
