package services

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// waitForQuiescence
// ---------------------------------------------------------------------------

// TestWaitForQuiescence_DetectsQuiescence verifies that waitForQuiescence returns
// as soon as the channel is quiet for the quietFor window, not after the full
// timeout. This guards against the regression where the goroutine that signals
// the channel started AFTER waitForQuiescence was called, causing a blind
// 500 ms wait on every connection.
func TestWaitForQuiescence_DetectsQuiescence(t *testing.T) {
	t.Parallel()

	ch := make(chan struct{}, 16)

	// Sender fires at 20 ms and 40 ms, then goes silent.
	// quietFor = 60 ms means quiescence triggers ~100 ms after start.
	// timeout = 500 ms — the test must complete well before that.
	go func() {
		time.Sleep(20 * time.Millisecond)
		ch <- struct{}{}
		time.Sleep(20 * time.Millisecond)
		ch <- struct{}{}
		// then silent
	}()

	start := time.Now()
	waitForQuiescence(ch, 500*time.Millisecond, 60*time.Millisecond)
	elapsed := time.Since(start)

	// Should return around 40 ms (last signal) + 60 ms (quiet window) = ~100 ms.
	// Allow generous headroom for slow CI runners, but must be well under 500 ms.
	if elapsed >= 450*time.Millisecond {
		t.Errorf("waitForQuiescence did not detect quiescence: elapsed %v (expected < 450ms)", elapsed)
	}
}

// TestWaitForQuiescence_TimesOut verifies the timeout path when the channel
// never goes quiet.
func TestWaitForQuiescence_TimesOut(t *testing.T) {
	t.Parallel()

	ch := make(chan struct{}, 16)

	// Keep sending so the quiet window never triggers.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				select {
				case ch <- struct{}{}:
				default:
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	timeout := 100 * time.Millisecond
	start := time.Now()
	waitForQuiescence(ch, timeout, 50*time.Millisecond)
	elapsed := time.Since(start)
	close(stop)

	if elapsed < timeout-10*time.Millisecond {
		t.Errorf("waitForQuiescence returned too early: elapsed %v (expected >= %v)", elapsed, timeout)
	}
}

// ---------------------------------------------------------------------------
// prepareSnapshotContent
// ---------------------------------------------------------------------------

// TestPrepareSnapshotContent_NormalisesLF verifies that bare \n is converted to
// \r\n so that xterm.js rows start at column 0 even when LNM is off (which
// DECSTR guarantees before the snapshot prefix). Without this, capture-pane
// output produces a staircase effect.
func TestPrepareSnapshotContent_NormalisesLF(t *testing.T) {
	t.Parallel()

	input := "line1\nline2\nline3"
	got := prepareSnapshotContent(input)
	want := "line1\r\nline2\r\nline3"

	if got != want {
		t.Errorf("prepareSnapshotContent LF normalisation:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestPrepareSnapshotContent_DoesNotDoubleCR verifies that existing \r\n pairs
// are not duplicated into \r\r\n.
func TestPrepareSnapshotContent_DoesNotDoubleCR(t *testing.T) {
	t.Parallel()

	input := "line1\r\nline2\r\n"
	got := prepareSnapshotContent(input)
	if strings.Contains(got, "\r\r\n") {
		t.Errorf("prepareSnapshotContent doubled CR: got %q", got)
	}
}

// TestPrepareSnapshotContent_PreservesSGR verifies that SGR color sequences
// (e.g. \x1b[32m green, \x1b[0m reset) survive sanitization. These are
// intentionally NOT stripped — only context-dependent positioning is removed.
func TestPrepareSnapshotContent_PreservesSGR(t *testing.T) {
	t.Parallel()

	input := "\x1b[32mgreen text\x1b[0m\n"
	got := prepareSnapshotContent(input)

	if !strings.Contains(got, "\x1b[32m") {
		t.Errorf("prepareSnapshotContent stripped SGR color: got %q", got)
	}
	if !strings.Contains(got, "\x1b[0m") {
		t.Errorf("prepareSnapshotContent stripped SGR reset: got %q", got)
	}
}

// TestPrepareSnapshotContent_StripsPositionalCodes verifies that absolute cursor
// positioning, screen clears, private mode switches, and DEC save/restore
// cursor sequences are removed. These codes assume prior terminal state and
// produce garbled output when replayed in a fresh xterm.js terminal.
func TestPrepareSnapshotContent_StripsPositionalCodes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
	}{
		{"absolute CUP", "\x1b[10;5Htext"},
		{"ED2 clear screen", "\x1b[2Jtext"},
		{"alt screen enter", "\x1b[?1049htext"},
		{"alt screen exit", "\x1b[?1049ltext"},
		{"cursor hide", "\x1b[?25ltext"},
		{"DEC save cursor", "\x1b7text"},
		{"DEC restore cursor", "\x1b8text"},
		{"CSI save cursor", "\x1b[stext"},
		{"CSI restore cursor", "\x1b[utext"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := prepareSnapshotContent(tc.input)
			if !strings.Contains(got, "text") {
				t.Errorf("prepareSnapshotContent removed visible text: got %q", got)
			}
			// The stripped sequence itself must not appear in the output
			// (test indirectly: if only "text" remains after normalisation, the escape was removed)
			if got != "text\r\n" && got != "text" {
				// Allow trailing \r\n from LF normalisation only
				stripped := strings.TrimSuffix(got, "\r\n")
				stripped = strings.TrimSuffix(stripped, "\r")
				if stripped != "text" {
					t.Errorf("unexpected content after stripping: got %q (input %q)", got, tc.input)
				}
			}
		})
	}
}

// TestFormatSnapshotForClient_IncludesPrefix verifies that formatSnapshotForClient
// prepends the caller-supplied prefix (e.g. ansiSnapshotPrefix / clearAndHome)
// and applies prepareSnapshotContent. This is the single choke-point used by
// both the control-mode and capture-pane streaming paths.
func TestFormatSnapshotForClient_IncludesPrefix(t *testing.T) {
	t.Parallel()

	prefix := "\x1b[!p\x1b[2J\x1b[H" // ansiSnapshotPrefix
	content := "line1\nline2"

	// nil instance — withCursorSync is a no-op
	got := formatSnapshotForClient(prefix, content, nil)

	if !strings.HasPrefix(got, prefix) {
		t.Errorf("formatSnapshotForClient did not prepend prefix: got %q", got)
	}
	if !strings.Contains(got, "line1\r\nline2") {
		t.Errorf("formatSnapshotForClient did not normalise LF: got %q", got)
	}
}
