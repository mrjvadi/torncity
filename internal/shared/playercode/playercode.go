// Package playercode is the public player code: the short identifier a player
// sees on their profile and gives to friends so they can be found.
//
// # Why there is a second identifier at all
//
// A player's record is keyed by a UUID, and that key is deliberately kept off
// every screen: it means nothing to a person, it is too long to read out or
// type, and once it is in a screenshot it circulates as though it were a fact
// about them. A player still needs something to hand a friend, so this is that
// thing — seven characters, safe to read aloud, and useless for anything but
// finding the player it belongs to.
//
// # The rules, and why each exists
//
//   - The alphabet leaves out 0/O and 1/I/L. A code is read off a phone screen
//     and typed on another one, often in a different script's keyboard
//     layout; every pair that looks alike is a lookup that fails for no reason
//     the player can see.
//   - Codes are RANDOM, drawn from crypto/rand. A sequential code would
//     publish the size of the player base and the order people joined in, and
//     it would make enumeration trivial: anyone could walk the codes and
//     collect every player. A random draw from 31^7 (about 27.5 billion)
//     leaves nothing to walk.
//   - A code is never all digits. A string of digits is a Telegram user id in
//     the search grammar (see handlers.ClassifyPlayerQuery), so a code that
//     could be all digits would make "/social 2345678" ambiguous. Refusing to
//     issue one removes the ambiguity at the source instead of resolving it
//     with a precedence rule someone has to remember.
//
// The alphabet and the length are repeated in exactly one other place: the
// CHECK constraint and the draw function of
// migrations/0007_player_public_code.up.sql. A test in this package reads that
// file and fails if the two drift apart.
package playercode

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Alphabet is every character a code may contain, in the order the draw
// indexes them: the digits 2-9 and the letters A-Z without I, L and O.
const Alphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// Length is the number of characters in a code.
const Length = 7

// rejectAbove is the first byte value the draw discards. It is the largest
// multiple of len(Alphabet) that fits in a byte (8 * 31 = 248): taking a byte
// modulo 31 without discarding 248-255 would make the first eight symbols of
// the alphabet slightly more likely than the rest, and a biased code is a
// guessable one.
const rejectAbove = (256 / len(Alphabet)) * len(Alphabet)

// ErrExhausted means the random source kept producing codes the rules refuse.
// With crypto/rand this does not happen; it guards against a broken or
// constant source looping forever.
var ErrExhausted = errors.New("playercode: the random source produced no usable code")

// maxDraws bounds New against a source that never yields an acceptable code.
// An honest source needs one draw 99.99% of the time (an all-digit code has
// probability (8/31)^7, under one in ten thousand).
const maxDraws = 64

// New draws a fresh code from crypto/rand.
func New() (string, error) {
	return Draw(rand.Reader)
}

// Draw draws a code from r. It is New with the source injected, so a test can
// pin the rejection rules with a scripted source.
func Draw(r io.Reader) (string, error) {
	var buf [16]byte
	for attempt := 0; attempt < maxDraws; attempt++ {
		code := make([]byte, 0, Length)
		for len(code) < Length {
			if _, err := io.ReadFull(r, buf[:]); err != nil {
				return "", fmt.Errorf("playercode: reading randomness: %w", err)
			}
			for _, b := range buf {
				if int(b) >= rejectAbove {
					continue
				}
				code = append(code, Alphabet[int(b)%len(Alphabet)])
				if len(code) == Length {
					break
				}
			}
		}
		if hasLetter(string(code)) {
			return string(code), nil
		}
	}
	return "", ErrExhausted
}

// Normalize returns s as a code would be stored: trimmed and upper-cased. It
// does not validate; see Valid.
//
// Upper-casing is safe because the alphabet has no lower-case letters, so a
// player typing "k7q2m9a" means exactly "K7Q2M9A".
func Normalize(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// Valid reports whether s, as given, is a well-formed code: exactly Length
// characters, all from Alphabet, and at least one of them a letter. Callers
// holding player input normalise it first.
func Valid(s string) bool {
	if len(s) != Length {
		return false
	}
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(Alphabet, s[i]) < 0 {
			return false
		}
	}
	return hasLetter(s)
}

// hasLetter reports whether s contains a character that is not a digit.
// Everything in Alphabet that is not 2-9 is a letter.
func hasLetter(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return true
		}
	}
	return false
}
