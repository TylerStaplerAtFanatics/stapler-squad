// Package ansi provides shared helpers for scanning ECMA-48/ANSI escape
// sequences. It exists to give the CSI final-byte range a single source of
// truth: the same [a-zA-Z]-only bug (missing '@', '~', and other non-letter
// terminators per ECMA-48) was independently reimplemented across the
// codebase (session/detection, session/detection/ratelimit, session/tmux,
// server/services) before being fixed in BUG-025. Callers should use this
// package instead of hand-rolling the character class again.
package ansi

import (
	"regexp"
	"strings"
)

// CSIFinalByteClass is the regex character class for a CSI sequence's final
// byte. Per ECMA-48, CSI final bytes span 0x40-0x7E ('@' through '~'), not
// just ASCII letters — sequences like Insert Character (CSI Ps @) or
// tilde-terminated function-key sequences would otherwise be left
// unstripped, leaking raw escape bytes into text that downstream pattern
// matching operates on.
const CSIFinalByteClass = `[@-~]`

// CSIRegex matches a complete, simple CSI sequence: ESC [ + parameter bytes
// + one final byte in CSIFinalByteClass. Callers that need to combine the
// CSI branch with other alternatives (OSC, charset designation, etc.) should
// build their own regexp using CSIFinalByteClass directly rather than
// duplicating the byte range.
var CSIRegex = regexp.MustCompile(`\x1b\[[0-9;]*` + CSIFinalByteClass)

// StripCSI removes CSI escape sequences from s. Inputs without an ESC byte
// take a fast path that avoids the regexp entirely.
func StripCSI(s string) string {
	if !strings.ContainsRune(s, '\x1b') {
		return s
	}
	return CSIRegex.ReplaceAllString(s, "")
}
