package credential

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238's default, which every authenticator app implements.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Time-based one-time codes (RFC 6238): six digits, a 30-second step,
// HMAC-SHA1 — the parameters every authenticator app reads from an
// otpauth:// URI without being told.
const (
	totpStep   = 30
	totpDigits = 6
	// totpSkew is how many steps either side of now are accepted, for a
	// phone whose clock is a little off.
	totpSkew = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a fresh 160-bit secret, base32 without padding.
func NewTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("credential: no randomness for a secret: %w", err)
	}
	return b32.EncodeToString(b), nil
}

// TOTPURI is the otpauth:// URI an authenticator app scans or imports.
func TOTPURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(totpStep))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// TOTPCode is the code for secret at t.
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("credential: malformed one-time secret: %w", err)
	}
	return hotp(key, uint64(t.Unix()/totpStep)), nil
}

// VerifyTOTP reports whether code is valid for secret at now, within the
// skew, comparing in constant time.
func VerifyTOTP(secret, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return false
	}
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return false
	}
	counter := now.Unix() / totpStep
	ok := 0
	for d := int64(-totpSkew); d <= totpSkew; d++ {
		ok |= subtle.ConstantTimeCompare([]byte(hotp(key, uint64(counter+d))), []byte(code))
	}
	return ok == 1
}

// hotp is RFC 4226's HOTP value.
func hotp(key []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, v%1_000_000)
}
