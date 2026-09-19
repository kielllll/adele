//go:build !windows

package player

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A fake mpv that dies instantly must produce an error naming the exit
// (with its stderr), not a bare "connection refused" after the dial loop.
func TestStartSurfacesEarlyExit(t *testing.T) {
	script := "#!/bin/sh\necho 'fake-mpv: boom' >&2\nexit 1\n"
	path := filepath.Join(t.TempDir(), "fake-mpv")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := start(path)
	if err == nil {
		t.Fatal("expected error for instantly-exiting binary")
	}
	msg := err.Error()
	if !strings.Contains(msg, "exited") {
		t.Fatalf("error should name the early exit, got: %q", msg)
	}
	if !strings.Contains(msg, "fake-mpv: boom") {
		t.Fatalf("error should carry stderr, got: %q", msg)
	}
}

// Full startup against real mpv: the reported bug (TCP listener never
// bound) failed here with "connection refused". Needs a real machine:
// sandboxes that block socket bind() cannot run it.
func TestNewStartsIPC(t *testing.T) {
	if os.Getenv("ADELE_LIVE_MPV") == "" {
		t.Skip("set ADELE_LIVE_MPV=1 to exercise real mpv startup")
	}
	p, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()
	if err := p.SetPaused(true); err != nil {
		t.Fatalf("SetPaused: %v", err)
	}
	if err := p.SetPaused(false); err != nil {
		t.Fatalf("resume: %v", err)
	}
}
