package terminal

// scanEscapeSequence returns the number of bytes, starting at b[start] (which
// must be the ESC byte 0x1b), that make up one complete ANSI/DEC escape
// sequence. It follows the same termination rules used by
// pkg/analytics/escape_code_parser.go:
//
//   - CSI (ESC [ ...)            terminates on a letter (A-Z, a-z)
//   - OSC (ESC ] ...)            terminates on BEL (0x07) or ST (ESC \)
//   - DCS/PM/APC/SOS
//     (ESC P/^/_/X ...)          terminate on ST (ESC \) or single-byte ST (0x9C)
//   - charset designation (ESC (, ), *, or + then a designator byte) is 3 bytes
//   - other simple escapes (ESC 7, ESC M, ESC c, C1-range second bytes, ...) are 2 bytes
//
// Treating any letter as a universal terminator (the previous behavior of
// this package) is only correct for CSI: OSC/DCS/PM/APC/SOS payloads (window
// titles, hyperlink URLs, base64 data, ...) routinely contain a letter well
// before their real terminator, which caused the tail of those payloads to
// be misread as visible terminal content.
//
// If a sequence runs off the end of b before it terminates, the remaining
// bytes are treated as consumed rather than risk splitting a sequence that
// is simply incomplete in this chunk. If b[start+1] is not a recognized
// sequence-type byte, only the stray ESC itself is consumed so the
// following byte is processed normally.
func scanEscapeSequence(b []byte, start int) int {
	if start >= len(b) || b[start] != '\x1b' {
		return 0
	}
	if start+1 >= len(b) {
		return len(b) - start
	}

	switch b[start+1] {
	case '[':
		return scanCSI(b, start)
	case ']':
		return scanUntilTerminator(b, start, true /* allowBEL */)
	case 'P', '^', '_', 'X':
		return scanUntilTerminator(b, start, false /* allowBEL */)
	case '(', ')', '*', '+':
		if start+2 < len(b) {
			return 3
		}
		return len(b) - start
	case '7', '8', 'D', 'E', 'H', 'M', 'N', 'O', 'Z', 'c':
		return 2
	default:
		if b[start+1] >= 0x40 && b[start+1] <= 0x5F {
			return 2
		}
		return 1
	}
}

// scanCSI scans a CSI sequence (ESC [ params... intermediates... final) and
// returns the total byte count from start, or the number of remaining bytes
// if the sequence is unterminated.
func scanCSI(b []byte, start int) int {
	i := start + 2
	for i < len(b) {
		c := b[i]
		switch {
		case c >= 0x30 && c <= 0x3F, c >= 0x20 && c <= 0x2F:
			i++
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z'):
			return i - start + 1
		default:
			// Not a valid CSI byte: treat the malformed prefix as consumed.
			return i - start
		}
	}
	return len(b) - start
}

// scanUntilTerminator scans an OSC/DCS/PM/APC/SOS sequence, terminating on
// ST (ESC \) or, when allowBEL is true, also on BEL (0x07); DCS/PM/APC/SOS
// additionally accept a single-byte ST (0x9C). Returns the total byte count
// from start, or the number of remaining bytes if unterminated.
func scanUntilTerminator(b []byte, start int, allowBEL bool) int {
	for i := start + 2; i < len(b); i++ {
		if allowBEL && b[i] == 0x07 {
			return i - start + 1
		}
		if b[i] == '\x1b' && i+1 < len(b) && b[i+1] == '\\' {
			return i - start + 2
		}
		if !allowBEL && b[i] == 0x9C {
			return i - start + 1
		}
	}
	return len(b) - start
}
