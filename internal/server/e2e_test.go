//go:build !windows

// Package server's end-to-end test wires the real handler to the real
// CmuxNotifier, pointing the notifier at a small shell fixture that records
// its argv. It exercises everything except the actual cmux binary.
package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zackey-heuristics/cmux-devcontainer-bridge/internal/notifier"
	"github.com/zackey-heuristics/cmux-devcontainer-bridge/internal/server"
)

// TestE2E_NotifyHappyPath runs the full request path with a fake cmux shim
// and verifies the argv the notifier actually produced.
func TestE2E_NotifyHappyPath(t *testing.T) {
	tmp := t.TempDir()
	capturePath := filepath.Join(tmp, "captured.txt")
	// fake-cmux.sh reads $FAKE_CMUX_CAPTURE from its environment; the bridge
	// passes the parent process environment through to the subprocess, so a
	// t.Setenv here is visible to the fixture.
	t.Setenv("FAKE_CMUX_CAPTURE", capturePath)

	fakeBin := fakeCmuxPath(t)
	n := notifier.NewCmuxNotifier(notifier.CmuxNotifierConfig{
		BinPath: fakeBin,
		Timeout: 3 * time.Second,
	})

	h := server.NewHandler(server.Config{
		MaxBodyBytes: 8192,
		DefaultTitle: "Claude Code",
		Notifier:     n,
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	payload := map[string]string{
		"title":        "E2E title",
		"subtitle":     "E2E subtitle",
		"body":         "E2E body",
		"workspace_id": "ws-e2e",
		"surface_id":   "sf-e2e",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(srv.URL+"/notify", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	lines := readCaptureLines(t, capturePath)
	want := []string{
		"notify",
		"--title", "E2E title",
		"--subtitle", "E2E subtitle",
		"--body", "E2E body",
		"--workspace", "ws-e2e",
		"--surface", "sf-e2e",
	}
	if len(lines) != len(want) {
		t.Fatalf("captured %d args, want %d\ngot:  %v\nwant: %v", len(lines), len(want), lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("arg[%d] = %q, want %q", i, lines[i], want[i])
		}
	}
}

// TestE2E_NotifierError_Returns502 verifies that a non-zero exit from the
// cmux binary surfaces as a 502 with a populated error field.
func TestE2E_NotifierError_Returns502(t *testing.T) {
	tmp := t.TempDir()
	capturePath := filepath.Join(tmp, "captured.txt")
	t.Setenv("FAKE_CMUX_CAPTURE", capturePath)
	t.Setenv("FAKE_CMUX_EXIT", "3")

	fakeBin := fakeCmuxPath(t)
	n := notifier.NewCmuxNotifier(notifier.CmuxNotifierConfig{
		BinPath: fakeBin,
		Timeout: 3 * time.Second,
	})
	h := server.NewHandler(server.Config{
		MaxBodyBytes: 8192,
		DefaultTitle: "Claude Code",
		Notifier:     n,
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/notify", "application/json",
		strings.NewReader(`{"title":"boom"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.OK {
		t.Error("ok = true, want false")
	}
	if body.Error == "" {
		t.Error("error field is empty")
	}
}

// TestE2E_DefaultTitleApplied verifies the default-title path when the
// request omits title.
func TestE2E_DefaultTitleApplied(t *testing.T) {
	tmp := t.TempDir()
	capturePath := filepath.Join(tmp, "captured.txt")
	t.Setenv("FAKE_CMUX_CAPTURE", capturePath)

	fakeBin := fakeCmuxPath(t)
	n := notifier.NewCmuxNotifier(notifier.CmuxNotifierConfig{BinPath: fakeBin})
	h := server.NewHandler(server.Config{
		MaxBodyBytes: 8192,
		DefaultTitle: "FallbackTitle",
		Notifier:     n,
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/notify", "application/json", strings.NewReader(`{"body":"hi"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	lines := readCaptureLines(t, capturePath)
	if len(lines) < 3 || lines[1] != "--title" || lines[2] != "FallbackTitle" {
		t.Errorf("default title not applied; got %v", lines)
	}
}

// fakeCmuxPath returns the absolute path to testdata/fake-cmux.sh and
// ensures it is executable (git may not preserve the +x bit on some
// workflows).
func fakeCmuxPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(wd, "testdata", "fake-cmux.sh")
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatalf("chmod %s: %v", p, err)
	}
	return p
}

// readCaptureLines reads the capture file the fake cmux wrote, retrying
// briefly to tolerate filesystem buffering on slower runners.
func readCaptureLines(t *testing.T, path string) []string {
	t.Helper()
	var data []byte
	for i := 0; i < 20; i++ {
		b, err := os.ReadFile(path)
		if err == nil && len(b) > 0 {
			data = b
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(data) == 0 {
		t.Fatalf("fake cmux did not write %s", path)
	}
	// Trim trailing newline only; interior newlines are the separators.
	s := strings.TrimRight(string(data), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
