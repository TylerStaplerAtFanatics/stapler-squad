// Package headless provides a subprocess-based interface for running claude -p
// headlessly. It manages session pools for prefix-cache reuse, streaming output,
// and clean subprocess lifecycle management.
package headless

import (
	"context"
	"errors"
	"io"

	"github.com/tstapler/stapler-squad/executor"
)

// StreamChunk is a single unit of output from a headless LLM call.
type StreamChunk struct {
	Text string
	Err  error
	Done bool
}

// ClaudeRunner abstracts how claude -p subprocesses are started.
// Implementors: ProcessRunner (real), FakeRunner (tests).
type ClaudeRunner interface {
	// Run starts claude -p with the given args and returns a ReadCloser for stdout,
	// a stop function to kill the process, and an error if the process fails to start.
	// The caller must call stop() to release resources even when the ReadCloser is drained.
	Run(ctx context.Context, args []string) (stdout io.ReadCloser, stop func() error, err error)
}

// Error sentinels returned in StreamChunk.Err or from CallBlocking.
var (
	// ErrClaudeNotFound is returned when the claude binary is not in PATH.
	ErrClaudeNotFound = errors.New("claude binary not found in PATH")
	// ErrLLMError is returned when claude exits with code 1 (LLM-level error).
	ErrLLMError = errors.New("claude LLM error (exit 1)")
	// ErrUsageError is returned when claude exits with code 2 (bad usage / bad flags).
	ErrUsageError = errors.New("claude usage error (exit 2)")
	// ErrInterrupted is returned when claude exits with code 130 (SIGINT).
	ErrInterrupted = errors.New("claude interrupted (exit 130)")
)

// ProcessRunner implements ClaudeRunner using executor.StartProcess.
type ProcessRunner struct {
	claudeBin string
	workDir   string // optional working directory; empty = inherit from parent
}

// WithWorkDir returns a copy of this ProcessRunner that sets the subprocess working
// directory to workDir. Used by CallBlockingWithOptions for per-call directory override.
func (r *ProcessRunner) WithWorkDir(workDir string) *ProcessRunner {
	return &ProcessRunner{claudeBin: r.claudeBin, workDir: workDir}
}

// Run starts the claude binary with args and returns a ReadCloser for stdout.
// The stop function terminates the subprocess and must always be called.
func (r *ProcessRunner) Run(ctx context.Context, args []string) (io.ReadCloser, func() error, error) {
	opts := []executor.ProcessOption{executor.WithNoControllingTerminal()}
	if r.workDir != "" {
		opts = append(opts, executor.WithProcessDir(r.workDir))
	}
	proc, err := executor.StartProcess(ctx, r.claudeBin, args, opts...)
	if err != nil {
		return nil, nil, err
	}

	stdout := proc.Stdout()
	stop := func() error {
		return proc.Stop()
	}
	return io.NopCloser(stdout), stop, nil
}
