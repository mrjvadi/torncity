// Package idempotency derives the key that makes a command safe to replay.
//
// Telegram redelivers updates and a player can tap the same inline button
// twice, so the same command reaches the system more than once. The spec
// (MASTER_PROMPT section 12) settles this by keying every command on
// request_id + player_id + idempotency_key: a second arrival with the same
// triple is the same intent and must not spend money, start a job or fire an
// event a second time.
//
// The key is a hash rather than the concatenated fields because it is stored
// per command and used as a cache and database key: a fixed 64-character value
// bounds index size and stops caller-supplied text from ending up verbatim in
// a key namespace.
package idempotency

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
)

// KeyLength is the length in characters of a derived key: sha256, hex encoded.
const KeyLength = sha256.Size * 2

// Key identifies one logical command attempt.
type Key string

// String returns the hex digest.
func (k Key) String() string { return string(k) }

// IsZero reports whether the key was never derived.
func (k Key) IsZero() bool { return k == "" }

// Derive builds the key for one command attempt.
//
// Every field is length-prefixed before it is hashed. Plain concatenation
// would make ("ab","c","d") and ("a","bc","d") hash identically, so two
// different commands would collide and the second one would be silently
// dropped as a duplicate. A separator byte would have the same flaw for any
// input that contains that byte, and player-supplied text can contain
// anything, so the boundary is encoded as a length instead.
func Derive(playerID, requestID, idempotencyKey string) Key {
	h := sha256.New()
	writeField(h, playerID)
	writeField(h, requestID)
	writeField(h, idempotencyKey)
	return Key(hex.EncodeToString(h.Sum(nil)))
}

// writeField writes one unambiguously delimited field into the hash.
func writeField(h hash.Hash, field string) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(field)))
	// hash.Hash never returns an error, per its documented contract.
	h.Write(n[:])
	h.Write([]byte(field))
}
