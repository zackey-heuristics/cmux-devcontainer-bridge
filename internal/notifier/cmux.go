package notifier

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"
)

// execRunner is the injectable seam used in tests to avoid real subprocess execution.
type execRunner func(ctx context.Context, bin string, args []string) error

// defaultExecRunner uses exec.CommandContext to run the real binary.
func defaultExecRunner(ctx context.Context, bin string, args []string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	// Inherit the parent process environment so that cmux can read
	// CMUX_SOCKET_PATH, CMUX_SOCKET_PASSWORD, and related vars.
	cmd.Env = os.Environ()
	return cmd.Run()
}

// CmuxNotifier delivers notifications by invoking the cmux CLI.
type CmuxNotifier struct {
	binPath string
	dryRun  bool
	timeout time.Duration
	run     execRunner
	logger  *slog.Logger
}

// CmuxNotifierConfig holds construction parameters for CmuxNotifier.
type CmuxNotifierConfig struct {
	// BinPath is the resolved absolute path to the cmux binary.
	BinPath string
	DryRun  bool
	// Timeout is how long to wait for cmux to complete. Zero uses 5s.
	Timeout time.Duration
	// Runner overrides the default exec runner; nil uses the real one.
	Runner execRunner
	Logger *slog.Logger
}

// NewCmuxNotifier constructs a CmuxNotifier from cfg.
func NewCmuxNotifier(cfg CmuxNotifierConfig) *CmuxNotifier {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	runner := cfg.Runner
	if runner == nil {
		runner = defaultExecRunner
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &CmuxNotifier{
		binPath: cfg.BinPath,
		dryRun:  cfg.DryRun,
		timeout: timeout,
		run:     runner,
		logger:  logger,
	}
}

// Notify builds the cmux argv and either dry-runs or execs the binary.
func (n *CmuxNotifier) Notify(ctx context.Context, p Payload) error {
	args := buildArgs(p)

	if n.dryRun {
		n.logger.Info("dry-run: would exec cmux", "bin", n.binPath, "args", args)
		return nil
	}

	n.logger.Debug("exec cmux", "bin", n.binPath, "args", args)

	execCtx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()

	err := n.run(execCtx, n.binPath, args)
	if err != nil {
		// Classify timeouts: only treat as ErrTimeout when the internal
		// deadline fired and the parent context is still live. A parent
		// cancellation (e.g. client disconnect) also makes execCtx.Err()
		// non-nil, but it is not a cmux timeout.
		if ctx.Err() == nil && execCtx.Err() == context.DeadlineExceeded {
			return &ErrTimeout{}
		}
		return fmt.Errorf("cmux notify failed: %w", err)
	}
	return nil
}

// buildArgs returns the argv slice for `cmux notify ...` given payload p.
// The binary itself is NOT included — the caller prepends it.
// The sub-command "notify" IS the first element.
func buildArgs(p Payload) []string {
	args := []string{"notify", "--title", p.Title}
	if p.Subtitle != "" {
		args = append(args, "--subtitle", p.Subtitle)
	}
	if p.Body != "" {
		args = append(args, "--body", p.Body)
	}
	if p.WorkspaceID != "" {
		args = append(args, "--workspace", p.WorkspaceID)
	}
	if p.SurfaceID != "" {
		args = append(args, "--surface", p.SurfaceID)
	}
	return args
}

// ResolveCmuxBin resolves the cmux binary path using the priority chain:
//  1. explicit (non-empty) string passed by the caller (--cmux-bin flag)
//  2. CMUX_BIN environment variable
//  3. exec.LookPath("cmux")
func ResolveCmuxBin(explicit string) (string, error) {
	if explicit != "" {
		if err := checkExecutable(explicit); err != nil {
			return "", &ErrBinaryNotFound{Msg: fmt.Sprintf("cmux binary not found at %q: %v", explicit, err)}
		}
		return explicit, nil
	}
	if env := os.Getenv("CMUX_BIN"); env != "" {
		if err := checkExecutable(env); err != nil {
			return "", &ErrBinaryNotFound{Msg: fmt.Sprintf("cmux binary not found at CMUX_BIN=%q: %v", env, err)}
		}
		return env, nil
	}
	p, err := exec.LookPath("cmux")
	if err != nil {
		return "", &ErrBinaryNotFound{Msg: "cmux binary not found in PATH"}
	}
	return p, nil
}

// checkExecutable returns an error if path does not exist or is not executable.
func checkExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("%s is not executable", path)
	}
	return nil
}
