package postgres

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// newUUID returns a random RFC 4122 version 4 UUID in canonical text form.
//
// Several tables in docs/database.md declare a uuid primary key with no
// database-side default (players.id, player_bot_links.id,
// idempotency_keys.id), so the identifier has to be produced by the writer.
// It is generated here rather than taken from the database because a caller
// often needs the id before the row exists — to reference it in an outbox
// payload written in the same transaction, for instance.
//
// crypto/rand is used rather than math/rand: these ids appear in logs and in
// event payloads, and a predictable sequence would let an observer enumerate
// records that were never meant to be guessable.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("postgres: generating uuid: %w", err)
	}

	// Version 4 in the high nibble of byte 6, RFC 4122 variant in the top two
	// bits of byte 8. Without these the value is 128 random bits but not a
	// UUID, and PostgreSQL would still accept it while every tool that parses
	// a version would disagree about what it is.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return formatUUID(b), nil
}

// formatUUID renders 16 bytes as 8-4-4-4-12 lowercase hex. Split out from
// newUUID so the encoding can be tested against known bytes without depending
// on the randomness.
func formatUUID(b [16]byte) string {
	var buf [36]byte

	hex.Encode(buf[0:8], b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], b[10:16])

	return string(buf[:])
}

// ensureID returns id, or a freshly generated one when id is empty. Callers
// may supply their own identifier (a command handler that already minted one),
// and must not be forced to.
func ensureID(id string) (string, error) {
	if id != "" {
		return id, nil
	}
	return newUUID()
}
