package action

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labostack/prox/internal/config"
)

// Serve is an action that serves files from a directory or a single file.
type Serve struct {
	handler http.Handler
}

// NewServe creates a file-serving handler.
func NewServe(act *config.Action, routePath string) (*Serve, error) {
	if act.File != "" {
		return newServeFile(act.File)
	}
	return newServeDir(act.Root, routePath)
}

// newServeFile serves a single file regardless of the request path.
func newServeFile(path string) (*Serve, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	return &Serve{
		handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f, err := os.Open(absPath)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			defer f.Close()

			info, err := f.Stat()
			if err != nil {
				http.NotFound(w, r)
				return
			}

			http.ServeContent(w, r, filepath.Base(absPath), info.ModTime(), f)
		}),
	}, nil
}

// newServeDir serves files from a directory with automatic prefix stripping.
func newServeDir(root, routePath string) (*Serve, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	fs := http.FileServer(neuteredFileSystem{http.Dir(absRoot)})

	// Calculate the prefix to strip based on the route path.
	// "/static/*" → strip "/static/"
	// "/*"        → strip "/"
	prefix := strings.TrimSuffix(routePath, "*")
	if prefix == "" {
		prefix = "/"
	}

	var handler http.Handler
	if prefix != "/" {
		handler = http.StripPrefix(prefix, fs)
	} else {
		handler = fs
	}

	return &Serve{handler: handler}, nil
}

func (s *Serve) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// neuteredFileSystem disables directory listings.
// Directories without index.html return 404.
type neuteredFileSystem struct {
	fs http.FileSystem
}

func (nfs neuteredFileSystem) Open(path string) (http.File, error) {
	f, err := nfs.fs.Open(path)
	if err != nil {
		return nil, err
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	// If it's a directory, try to serve index.html instead.
	if info.IsDir() {
		index := filepath.Join(path, "index.html")
		indexFile, err := nfs.fs.Open(index)
		if err != nil {
			f.Close()
			return nil, os.ErrNotExist
		}
		indexFile.Close()
	}

	return f, nil
}
