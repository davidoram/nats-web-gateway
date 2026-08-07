package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadinessMarkerTracksAvailability(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "ready")
	marker := &readinessMarker{path: path}

	if err := marker.set(false); err != nil {
		t.Fatalf("clear absent marker: %v", err)
	}
	if err := marker.set(true); err != nil {
		t.Fatalf("set ready: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if string(contents) != "ready\n" {
		t.Fatalf("marker contents = %q, want ready newline", contents)
	}
	if err := marker.set(false); err != nil {
		t.Fatalf("set unavailable: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("marker stat after unavailable = %v, want not exist", err)
	}
}
