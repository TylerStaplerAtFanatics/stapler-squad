// Package ansi provides shared helpers for scanning ECMA-48/ANSI escape
// sequences. It exists to give the CSI final-byte range a single source of
// truth: the same [a-zA-Z]-only bug (missing '@', '~', and other non-letter
// terminators per ECMA-48) was independently reimplemented across the
// codebase (session/detection, session/detection/ratelimit, session/tmux,
// server/services, pkg/analytics) before being fixed in BUG-025. Callers
// should use this package instead of hand-rolling the character class or
// byte range again — CSIFinalByteClass/CSIRegex/StripCSI for regex-based
// scanners, IsCSIFinalByte for manual byte-level scanners (e.g.
// pkg/analytics/escape_code_parser.go).
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

// CSIFinalByteMin and CSIFinalByteMax bound the same ECMA-48 CSI final-byte
// range as CSIFinalByteClass, for callers doing manual byte-range scanning
// instead of building a regex (e.g. a hand-rolled state-machine parser).
const (
	CSIFinalByteMin byte = 0x40
	CSIFinalByteMax byte = 0x7E
)

// IsCSIFinalByte reports whether b is a valid CSI sequence final byte per
// ECMA-48 (0x40-0x7E). Byte-level twin of CSIFinalByteClass for parsers that
// scan byte-by-byte rather than via regexp.
func IsCSIFinalByte(b byte) bool {
	return b >= CSIFinalByteMin && b <= CSIFinalByteMax
}

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
