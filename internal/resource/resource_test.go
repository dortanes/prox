package resource

import (
	"os"
	"testing"

	"github.com/dortanes/prox/internal/config"
)

func TestResolver_TextResource(t *testing.T) {
	r := NewResolver(map[string]*config.Resource{
		"greeting": {Text: "Hello!"},
	})

	data, err := r.Resolve("greeting")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "Hello!" {
		t.Errorf("expected 'Hello!', got %q", string(data))
	}
}

func TestResolver_FileResource(t *testing.T) {
	// Create a temporary file for testing
	tmpFile := t.TempDir() + "/test.txt"
	if err := os.WriteFile(tmpFile, []byte("File Content!"), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	r := NewResolver(map[string]*config.Resource{
		"file_res": {File: tmpFile},
	})

	data, err := r.Resolve("file_res")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "File Content!" {
		t.Errorf("expected 'File Content!', got %q", string(data))
	}
}

func TestResolver_NotFound(t *testing.T) {
	r := NewResolver(nil)

	_, err := r.Resolve("missing")
	if err == nil {
		t.Fatal("expected error for missing resource")
	}
}

func TestResolver_EmptyContent(t *testing.T) {
	r := NewResolver(map[string]*config.Resource{
		"empty": {},
	})

	_, err := r.Resolve("empty")
	if err == nil {
		t.Fatal("expected error for empty resource")
	}
}
