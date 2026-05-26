package headless

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// CallOptions configures an individual pool call with overrides.
type CallOptions struct {
	// WorkDir sets the subprocess working directory (for git operations).
	WorkDir string
	// Model overrides the pool's DefaultModel for this call only.
	Model string
	// TimeoutSecs is unused by Pool directly — callers wrap ctx with WithTimeout.
	TimeoutSecs int
}

// firstCallJSONResult is the JSON schema returned by claude -p --output-format json.
type firstCallJSONResult struct {
	SessionID string  `json:"session_id"`
	Result    string  `json:"result"`
	CostUSD   float64 `json:"cost_usd"`
}

// NewPool constructs a Pool by looking up the claude binary in PATH.
// Returns ErrClaudeNotFound if the binary is not found.
func NewPool(cfg PoolConfig) (*Pool, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrClaudeNotFound, err)
	}
	applyDefaults(&cfg)
	runner := &ProcessRunner{claudeBin: bin}
	return newPoolWithRunner(cfg, runner, bin), nil
}

// NewPoolWithRunner constructs a Pool with a custom runner (no PATH lookup).
// Used in tests to inject a FakeRunner.
func NewPoolWithRunner(cfg PoolConfig, runner ClaudeRunner) *Pool {
	applyDefaults(&cfg)
	return newPoolWithRunner(cfg, runner, "claude")
}

func applyDefaults(cfg *PoolConfig) {
	if cfg.MaxCallsPerSession <= 0 {
		cfg.MaxCallsPerSession = defaultMaxCalls
	}
	if cfg.MaxConcurrentSessions <= 0 {
		cfg.MaxConcurrentSessions = defaultMaxConcurrent
	}
}

func newPoolWithRunner(cfg PoolConfig, runner ClaudeRunner, claudeBin string) *Pool {
	return &Pool{
		claudeBin:      claudeBin,
		cfg:            cfg,
		runner:         runner,
		sessions:       make(map[FeatureKey]*sessionState),
		keyMu:          make(map[FeatureKey]*sync.Mutex),
		concurrencySem: make(chan struct{}, cfg.MaxConcurrentSessions),
	}
}

// acquireSession reads current session state for key and builds the subprocess args.
// It increments callCount under lock before returning.
// Returns isFirstCall=true when this call should use --output-format json.
//
// IMPORTANT: the per-key mutex is held only long enough to read/write state —
// it is NOT held during subprocess execution to avoid deadlocks.
func (p *Pool) acquireSession(key FeatureKey, systemPrompt, model string) (isFirstCall bool, args []string) {
	p.mu.Lock()
	keyMu := p.acquireKeyMu(key)
	if _, ok := p.sessions[key]; !ok {
		p.sessions[key] = &sessionState{}
	}
	p.mu.Unlock()

	keyMu.Lock()
	defer keyMu.Unlock()

	p.mu.Lock()
	state := p.sessions[key]

	// Determine if we need a fresh session (first call or rotation due to errors/max calls).
	needsRotation := state.sessionID == "" ||
		state.callCount >= p.cfg.MaxCallsPerSession ||
		state.consecutiveErrors >= maxConsecutiveErrors

	if needsRotation && state.sessionID != "" {
		// Reset the state in place.
		state.sessionID = ""
		state.callCount = 0
		state.consecutiveErrors = 0
	}

	sessionID := state.sessionID
	state.callCount++
	p.mu.Unlock()

	// Effective model: per-call override > pool default.
	effectiveModel := model
	if effectiveModel == "" {
		effectiveModel = p.cfg.DefaultModel
	}

	if sessionID == "" {
		// First call: JSON output to capture session_id.
		isFirstCall = true
		args = []string{"-p", "--output-format", "json", "--system-prompt", systemPrompt, "--exclude-dynamic-system-prompt-sections"}
		if effectiveModel != "" {
			args = append(args, "--model", effectiveModel)
		}
	} else {
		// Resumed call: plain output (line-at-a-time streaming).
		isFirstCall = false
		args = []string{"-p", "--resume", sessionID, "--exclude-dynamic-system-prompt-sections"}
	}

	return isFirstCall, args
}

// storeSessionID stores the session ID captured from a first-call JSON response.
func (p *Pool) storeSessionID(key FeatureKey, sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if state, ok := p.sessions[key]; ok {
		state.sessionID = sessionID
		state.consecutiveErrors = 0
	}
}

// recordSuccess resets the consecutive error counter for key.
func (p *Pool) recordSuccess(key FeatureKey) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if state, ok := p.sessions[key]; ok {
		state.consecutiveErrors = 0
	}
}

// recordError increments the consecutive error counter for key.
// Returns true if the circuit breaker threshold has been reached.
func (p *Pool) recordError(key FeatureKey) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if state, ok := p.sessions[key]; ok {
		state.consecutiveErrors++
		return state.consecutiveErrors >= maxConsecutiveErrors
	}
	return false
}

// Call starts a streaming headless LLM call for the given feature key.
// It returns a channel that receives StreamChunk values. The channel is closed
// when the subprocess exits (or the context is cancelled).
//
// The caller should drain the channel until Done=true or Err!=nil.
func (p *Pool) Call(ctx context.Context, key FeatureKey, systemPrompt, userPrompt string) (<-chan StreamChunk, error) {
	isFirstCall, baseArgs := p.acquireSession(key, systemPrompt, p.cfg.DefaultModel)

	// Append user prompt as the final argument.
	args := append(baseArgs, userPrompt)

	ch := make(chan StreamChunk, 16)

	// Acquire concurrency semaphore before starting the subprocess.
	p.concurrencySem <- struct{}{}

	stdout, stop, err := p.runner.Run(ctx, args)
	if err != nil {
		<-p.concurrencySem // release on startup failure
		if tripBreaker := p.recordError(key); tripBreaker {
			p.rotateSession(key)
		}
		close(ch)
		return ch, fmt.Errorf("headless runner start: %w", err)
	}

	go func() {
		defer close(ch)
		defer func() { _ = stop() }()
		defer func() { <-p.concurrencySem }()

		send := func(chunk StreamChunk) bool {
			select {
			case ch <- chunk:
				return true
			case <-ctx.Done():
				return false
			}
		}

		if isFirstCall {
			// First call: accumulate all output and parse JSON.
			data, readErr := io.ReadAll(stdout)
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				if tripBreaker := p.recordError(key); tripBreaker {
					p.rotateSession(key)
				}
				send(StreamChunk{Err: readErr, Done: true})
				return
			}

			// Check for context cancellation before parsing.
			if ctx.Err() != nil {
				return
			}

			var result firstCallJSONResult
			if jsonErr := json.Unmarshal(data, &result); jsonErr != nil {
				// Not valid JSON: treat the whole output as plain text.
				text := strings.TrimSpace(string(data))
				if text != "" {
					if !send(StreamChunk{Text: text}) {
						return
					}
				}
				if tripBreaker := p.recordError(key); tripBreaker {
					p.rotateSession(key)
				}
				send(StreamChunk{Err: fmt.Errorf("first-call JSON parse: %w", jsonErr), Done: true})
				return
			}

			// Store the session ID for future resume calls.
			if result.SessionID != "" {
				p.storeSessionID(key, result.SessionID)
			}
			p.recordSuccess(key)
			if result.Result != "" {
				if !send(StreamChunk{Text: result.Result}) {
					return
				}
			}
			send(StreamChunk{Done: true})
			return
		}

		// Resumed call: stream line by line.
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if ctx.Err() != nil {
				return
			}
			line := scanner.Text()
			if !send(StreamChunk{Text: line}) {
				return
			}
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
			if tripBreaker := p.recordError(key); tripBreaker {
				p.rotateSession(key)
			}
			send(StreamChunk{Err: err, Done: true})
			return
		}
		p.recordSuccess(key)
		send(StreamChunk{Done: true})
	}()

	return ch, nil
}

// CallWithOptions is like Call but allows overriding model and working directory.
// If opts.WorkDir is non-empty and the underlying runner is a *ProcessRunner, a
// new runner with that workDir is used for this call only (bypasses pool session reuse
// since workDir changes invalidate session caching).
func (p *Pool) CallWithOptions(ctx context.Context, key FeatureKey, systemPrompt, userPrompt string, opts CallOptions) (<-chan StreamChunk, error) {
	if opts.WorkDir != "" {
		if pr, ok := p.runner.(*ProcessRunner); ok {
			dirRunner := pr.WithWorkDir(opts.WorkDir)
			oneShot := NewPoolWithRunner(PoolConfig{MaxCallsPerSession: 1, MaxConcurrentSessions: 1, DefaultModel: opts.Model}, dirRunner)
			return oneShot.Call(ctx, key, systemPrompt, userPrompt)
		}
	}
	return p.Call(ctx, key, systemPrompt, userPrompt)
}

// CallBlockingWithOptions is like CallBlocking but supports WorkDir and Model overrides.
func (p *Pool) CallBlockingWithOptions(ctx context.Context, key FeatureKey, systemPrompt, userPrompt string, opts CallOptions) (string, error) {
	ch, err := p.CallWithOptions(ctx, key, systemPrompt, userPrompt, opts)
	if err != nil {
		return "", err
	}
	return drainChannel(ch)
}

// CallBlocking runs a headless LLM call and blocks until the result is complete.
// Returns the concatenated text from all chunks and the first non-nil error.
func (p *Pool) CallBlocking(ctx context.Context, key FeatureKey, systemPrompt, userPrompt string) (string, error) {
	ch, err := p.Call(ctx, key, systemPrompt, userPrompt)
	if err != nil {
		return "", err
	}
	return drainChannel(ch)
}

// drainChannel collects all StreamChunk text from ch until Done=true or Err!=nil.
func drainChannel(ch <-chan StreamChunk) (string, error) {
	var sb strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			return sb.String(), chunk.Err
		}
		if chunk.Text != "" {
			sb.WriteString(chunk.Text)
		}
		if chunk.Done {
			break
		}
	}
	// Drain remaining chunks in case the channel has extras.
	for range ch {
	}
	return sb.String(), nil
}
