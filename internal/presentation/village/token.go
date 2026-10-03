package village

import (
	"strconv"
	"strings"
)

// LotToken is one lot's own compact callback argument: its coordinates,
// and whether the building being placed there is rotated — one token
// instead of three separate callback segments, so a 5x5 grid of buttons
// still fits Telegram's 64-byte callback_data budget with room to spare.
func LotToken(x, y int, rotated bool) string {
	t := coordToken(x) + "-" + coordToken(y)
	if rotated {
		t += "-r"
	}
	return t
}

// ParseLotToken reads a LotToken back. ok is false for anything malformed
// or negative — a forged or stale button, refused as firmly as any other
// tampered callback argument, never trusted as a coordinate on its own.
func ParseLotToken(s string) (x, y int, rotated, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, false, false
	}
	if strings.HasSuffix(s, "-r") {
		rotated = true
		s = strings.TrimSuffix(s, "-r")
	}
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false, false
	}
	xi, err1 := strconv.Atoi(parts[0])
	yi, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || xi < 0 || yi < 0 {
		return 0, 0, false, false
	}
	return xi, yi, rotated, true
}

// coordToken writes one coordinate: a negative one (land west or south of the
// first grid, docs/adr/0044) as "m" and its size, so the "-" between x and y
// stays the only dash.
func coordToken(v int) string {
	if v < 0 {
		return "m" + strconv.Itoa(-v)
	}
	return strconv.Itoa(v)
}

// ParseLotTokenAny reads a LotToken whose coordinates may be negative (land
// beyond the first grid). Where a handler indexes the first grid it must keep
// to ParseLotToken, which refuses them.
func ParseLotTokenAny(s string) (x, y int, rotated, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, false, false
	}
	if strings.HasSuffix(s, "-r") {
		rotated = true
		s = strings.TrimSuffix(s, "-r")
	}
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false, false
	}
	xi, ok1 := parseCoord(parts[0])
	yi, ok2 := parseCoord(parts[1])
	if !ok1 || !ok2 {
		return 0, 0, false, false
	}
	return xi, yi, rotated, true
}

func parseCoord(s string) (int, bool) {
	neg := false
	if strings.HasPrefix(s, "m") {
		neg, s = true, s[1:]
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 || (neg && v == 0) || v > 1<<24 {
		return 0, false
	}
	if neg {
		v = -v
	}
	return v, true
}
