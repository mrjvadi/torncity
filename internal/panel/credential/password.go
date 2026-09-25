// Package credential holds what proves a panel operator is who they say:
// the argon2id password hash and the time-based one-time code. It is shared
// by the server-side account tool (cmd/admin panel user) and the panel's
// sign-in (cmd/panel), so both hash and check in exactly one way.
package credential

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// The argon2id parameters, per RFC 9106's second recommended option
// (64 MiB, 3 passes) with 2 lanes. They are written into every hash, so
// raising them later still verifies the older hashes.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16
)

// maxPasswordBytes bounds what is hashed: a megabyte "password" is a way to
// make the server spend its memory, not a password.
const maxPasswordBytes = 256

// ErrMalformedHash is a stored hash this package did not write.
var ErrMalformedHash = errors.New("credential: malformed password hash")

// ErrPasswordTooLong is a password over maxPasswordBytes.
var ErrPasswordTooLong = errors.New("credential: password too long")

// HashPassword returns the PHC string of password under a fresh salt.
func HashPassword(password string) (string, error) {
	if len(password) > maxPasswordBytes {
		return "", ErrPasswordTooLong
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("credential: no randomness for a salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the PHC string, comparing
// in constant time.
func VerifyPassword(encoded, password string) (bool, error) {
	if len(password) > maxPasswordBytes {
		return false, nil
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrMalformedHash
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil ||
		memory == 0 || memory > 1<<20 || iterations == 0 || iterations > 16 || threads == 0 {
		return false, ErrMalformedHash
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return false, ErrMalformedHash
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil || len(want) < 16 {
		return false, ErrMalformedHash
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash is verified against when no account has the name typed, so a
// wrong username costs the same time as a wrong password. It is made on
// first use, not at start-up.
var dummyHash = sync.OnceValue(func() string {
	h, err := HashPassword("no account has this password")
	if err != nil {
		panic(err)
	}
	return h
})

// BurnTime spends the time one verification takes, for an unknown account.
func BurnTime(password string) {
	_, _ = VerifyPassword(dummyHash(), password)
}
