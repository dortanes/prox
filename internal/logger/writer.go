package logger

import (
	"os"
	"sync"
)

// FileWriter is a thread-safe file writer that supports reopen for log rotation.
// Designed to work with external rotation tools (e.g. logrotate) via SIGHUP.
type FileWriter struct {
	path string
	mu   sync.Mutex
	file *os.File
}

// NewFileWriter opens a file in append mode for writing log entries.
func NewFileWriter(path string) (*FileWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &FileWriter{path: path, file: f}, nil
}

func (w *FileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Write(p)
}

// Reopen closes and re-opens the file. This allows external log rotation
// tools to rename the file and have the writer create a fresh one.
func (w *FileWriter) Reopen() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		w.file.Close()
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	w.file = f
	return nil
}

// Close flushes and closes the underlying file.
func (w *FileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}
