package resource

import (
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
