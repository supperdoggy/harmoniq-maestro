package acquisition

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const defaultCommandTimeout = 15 * time.Minute

const (
	maxCommandStdoutBytes = 4 << 20
	maxCommandStderrBytes = 64 << 10
)

// CommandResult captures machine-readable stdout and diagnostic stderr
// separately.
type CommandResult struct {
	Stdout []byte
	Stderr []byte
}

// CommandRunner is the process boundary used by providers. Tests can inject a
// runner without executing downloader binaries.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
}

// ExecCommandRunner executes a binary directly, without a shell.
type ExecCommandRunner struct{}

// Run implements CommandRunner using exec.CommandContext.
func (ExecCommandRunner) Run(
	ctx context.Context,
	name string,
	args ...string,
) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	configureProcessGroup(cmd)

	stdout := newCappedBuffer(maxCommandStdoutBytes)
	stderr := newCappedBuffer(maxCommandStderrBytes)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return CommandResult{
		Stdout: stdout.Bytes(),
		Stderr: stderr.Bytes(),
	}, err
}

// cappedBuffer retains at most limit bytes while reporting every input byte as
// consumed. This lets os/exec continue draining a noisy child process without
// allowing its captured output to grow without bound.
type cappedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func newCappedBuffer(limit int) cappedBuffer {
	return cappedBuffer{limit: limit}
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	consumed := len(value)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		return consumed, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
	}
	_, _ = b.buffer.Write(value)
	return consumed, nil
}

func (b *cappedBuffer) Bytes() []byte {
	return b.buffer.Bytes()
}

// CommandError adds bounded downloader diagnostics without exposing an
// unbounded process log.
type CommandError struct {
	Provider ProviderName
	Err      error
	Stderr   string
}

func (e *CommandError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("%s command failed: %v", e.Provider, e.Err)
	}
	return fmt.Sprintf("%s command failed: %v: %s", e.Provider, e.Err, e.Stderr)
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

func runCommand(
	ctx context.Context,
	timeout time.Duration,
	provider ProviderName,
	runner CommandRunner,
	binary string,
	args ...string,
) (CommandResult, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := runner.Run(commandCtx, binary, args...)
	if err == nil {
		return result, nil
	}

	if commandCtx.Err() != nil {
		return result, fmt.Errorf("%s command stopped: %w", provider, commandCtx.Err())
	}

	return result, &CommandError{
		Provider: provider,
		Err:      err,
		Stderr:   boundedDiagnostic(result.Stderr),
	}
}

func normalizeTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultCommandTimeout
	}
	return timeout
}

func normalizeRunner(runner CommandRunner) CommandRunner {
	if runner == nil {
		return ExecCommandRunner{}
	}
	return runner
}

func boundedDiagnostic(stderr []byte) string {
	const maxDiagnosticBytes = 4096

	diagnostic := strings.TrimSpace(string(stderr))
	if len(diagnostic) <= maxDiagnosticBytes {
		return diagnostic
	}
	return diagnostic[:maxDiagnosticBytes] + "…"
}
