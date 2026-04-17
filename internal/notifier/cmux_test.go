package notifier

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// captureRunner records the argv it receives and returns a configurable error.
type captureRunner struct {
	gotBin  string
	gotArgs []string
	err     error
}

func (c *captureRunner) run(ctx context.Context, bin string, args []string) error {
	c.gotBin = bin
	c.gotArgs = args
	return c.err
}

func makeNotifier(runner execRunner) *CmuxNotifier {
	return NewCmuxNotifier(CmuxNotifierConfig{
		BinPath: "/usr/local/bin/cmux",
		Runner:  runner,
		Timeout: 5 * time.Second,
	})
}

// TestBuildArgs_TitleOnly verifies that a payload with only Title emits the minimal argv.
func TestBuildArgs_TitleOnly(t *testing.T) {
	args := buildArgs(Payload{Title: "Hi"})
	want := []string{"notify", "--title", "Hi"}
	assertArgs(t, args, want)
}

// TestBuildArgs_TitleAndBody verifies that Body appends --body.
func TestBuildArgs_TitleAndBody(t *testing.T) {
	args := buildArgs(Payload{Title: "Hi", Body: "details"})
	want := []string{"notify", "--title", "Hi", "--body", "details"}
	assertArgs(t, args, want)
}

// TestBuildArgs_TitleAndSubtitle verifies that Subtitle appends --subtitle.
func TestBuildArgs_TitleAndSubtitle(t *testing.T) {
	args := buildArgs(Payload{Title: "Hi", Subtitle: "sub"})
	want := []string{"notify", "--title", "Hi", "--subtitle", "sub"}
	assertArgs(t, args, want)
}

// TestBuildArgs_TitleAndWorkspace verifies that WorkspaceID appends --workspace.
func TestBuildArgs_TitleAndWorkspace(t *testing.T) {
	args := buildArgs(Payload{Title: "Hi", WorkspaceID: "ws1"})
	want := []string{"notify", "--title", "Hi", "--workspace", "ws1"}
	assertArgs(t, args, want)
}

// TestBuildArgs_TitleAndSurface verifies that SurfaceID appends --surface.
func TestBuildArgs_TitleAndSurface(t *testing.T) {
	args := buildArgs(Payload{Title: "Hi", SurfaceID: "sf1"})
	want := []string{"notify", "--title", "Hi", "--surface", "sf1"}
	assertArgs(t, args, want)
}

// TestBuildArgs_AllFields verifies the canonical ordering when all fields are set.
func TestBuildArgs_AllFields(t *testing.T) {
	p := Payload{
		Title:       "Hi",
		Subtitle:    "sub",
		Body:        "body text",
		WorkspaceID: "ws1",
		SurfaceID:   "sf1",
	}
	args := buildArgs(p)
	want := []string{
		"notify", "--title", "Hi",
		"--subtitle", "sub",
		"--body", "body text",
		"--workspace", "ws1",
		"--surface", "sf1",
	}
	assertArgs(t, args, want)
}

// TestBuildArgs_EmptyOptionals verifies that empty optional fields produce no flags.
func TestBuildArgs_EmptyOptionals(t *testing.T) {
	args := buildArgs(Payload{Title: "Hi", Subtitle: "", Body: "", WorkspaceID: "", SurfaceID: ""})
	want := []string{"notify", "--title", "Hi"}
	assertArgs(t, args, want)
}

// TestBuildArgs_FlagLikeTitleIsArgvSafe verifies that a title starting with "--"
// is passed as a plain argv element (not a flag) due to argv separation.
func TestBuildArgs_FlagLikeTitleIsArgvSafe(t *testing.T) {
	args := buildArgs(Payload{Title: "--foo"})
	// The value "--foo" must appear at index 2 (the arg of --title), not as a key.
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d: %v", len(args), args)
	}
	if args[1] != "--title" {
		t.Errorf("args[1] = %q, want --title", args[1])
	}
	if args[2] != "--foo" {
		t.Errorf("args[2] = %q, want --foo", args[2])
	}
}

// TestNotify_HappyPath verifies that Notify calls the runner with the right bin and args.
func TestNotify_HappyPath(t *testing.T) {
	cr := &captureRunner{}
	n := makeNotifier(cr.run)
	p := Payload{Title: "Test"}
	if err := n.Notify(context.Background(), p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cr.gotBin != "/usr/local/bin/cmux" {
		t.Errorf("bin = %q, want /usr/local/bin/cmux", cr.gotBin)
	}
	if len(cr.gotArgs) < 3 || cr.gotArgs[0] != "notify" {
		t.Errorf("args[0] = %q, want notify", cr.gotArgs[0])
	}
}

// TestNotify_RunnerError verifies that a runner error is wrapped and returned.
func TestNotify_RunnerError(t *testing.T) {
	cr := &captureRunner{err: errors.New("exec failed")}
	n := makeNotifier(cr.run)
	err := n.Notify(context.Background(), Payload{Title: "Test"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestNotify_ContextCancellation verifies that a cancelled context produces an ErrTimeout.
func TestNotify_ContextCancellation(t *testing.T) {
	// Use a very short timeout so the deadline fires before the runner completes.
	n := NewCmuxNotifier(CmuxNotifierConfig{
		BinPath: "/usr/local/bin/cmux",
		Timeout: 1 * time.Nanosecond, // effectively immediate
		Runner: func(ctx context.Context, bin string, args []string) error {
			// Wait until the context is cancelled.
			<-ctx.Done()
			return ctx.Err()
		},
	})
	err := n.Notify(context.Background(), Payload{Title: "Test"})
	var errTimeout *ErrTimeout
	if !errors.As(err, &errTimeout) {
		t.Errorf("expected ErrTimeout, got %T: %v", err, err)
	}
}

// TestNotify_ParentCancellation verifies that a parent-context cancellation
// (e.g. client disconnect) is NOT classified as an ErrTimeout — it is a
// runner error. Monitoring callers rely on ErrTimeout meaning cmux actually
// hung.
func TestNotify_ParentCancellation(t *testing.T) {
	n := NewCmuxNotifier(CmuxNotifierConfig{
		BinPath: "/usr/local/bin/cmux",
		Timeout: 5 * time.Second, // plenty of budget
		Runner: func(ctx context.Context, bin string, args []string) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})
	parent, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Notify so the runner sees Canceled, not DeadlineExceeded
	err := n.Notify(parent, Payload{Title: "Test"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var errTimeout *ErrTimeout
	if errors.As(err, &errTimeout) {
		t.Errorf("parent cancellation was misclassified as ErrTimeout: %v", err)
	}
}

// TestNotify_DryRun verifies that dry-run mode does not call the runner.
func TestNotify_DryRun(t *testing.T) {
	called := false
	n := NewCmuxNotifier(CmuxNotifierConfig{
		BinPath: "/usr/local/bin/cmux",
		DryRun:  true,
		Runner: func(ctx context.Context, bin string, args []string) error {
			called = true
			return nil
		},
	})
	if err := n.Notify(context.Background(), Payload{Title: "Test"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("runner was called in dry-run mode")
	}
}

// TestResolveCmuxBin_ExplicitWins verifies that the explicit path takes priority.
func TestResolveCmuxBin_ExplicitWins(t *testing.T) {
	// Create a temporary executable file.
	dir := t.TempDir()
	bin := filepath.Join(dir, "cmux")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CMUX_BIN", "/nonexistent/cmux")

	got, err := ResolveCmuxBin(bin)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != bin {
		t.Errorf("got %q, want %q", got, bin)
	}
}

// TestResolveCmuxBin_EnvWins verifies that CMUX_BIN wins over PATH lookup.
func TestResolveCmuxBin_EnvWins(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "cmux")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CMUX_BIN", bin)

	got, err := ResolveCmuxBin("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != bin {
		t.Errorf("got %q, want %q", got, bin)
	}
}

// TestResolveCmuxBin_MissingBinary verifies that a missing binary returns ErrBinaryNotFound.
func TestResolveCmuxBin_MissingBinary(t *testing.T) {
	// Unset CMUX_BIN and ensure "cmux" is not in PATH by setting PATH to empty.
	t.Setenv("CMUX_BIN", "")
	// Save PATH, set to temp-only dir that has no cmux.
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	_, err := ResolveCmuxBin("")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var notFound *ErrBinaryNotFound
	if !errors.As(err, &notFound) {
		t.Errorf("expected ErrBinaryNotFound, got %T: %v", err, err)
	}
}

func assertArgs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(args) = %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
