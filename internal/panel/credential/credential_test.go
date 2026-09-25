package credential

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("unexpected hash format %q", h)
	}
	if strings.Contains(h, "horse") {
		t.Fatal("the hash carries the password")
	}
	if ok, err := VerifyPassword(h, "correct horse battery staple"); err != nil || !ok {
		t.Fatalf("right password refused: %v %v", ok, err)
	}
	if ok, _ := VerifyPassword(h, "correct horse battery stapl"); ok {
		t.Fatal("wrong password accepted")
	}
	h2, _ := HashPassword("correct horse battery staple")
	if h == h2 {
		t.Fatal("two hashes of one password share a salt")
	}
}

func TestPasswordMalformedAndLong(t *testing.T) {
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=65536,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=999999999,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA"} {
		if ok, err := VerifyPassword(bad, "x"); ok || err == nil {
			t.Errorf("%q: want a malformed-hash error, got %v %v", bad, ok, err)
		}
	}
	if _, err := HashPassword(strings.Repeat("a", 300)); err == nil {
		t.Fatal("a 300-byte password was hashed")
	}
}

// RFC 6238 appendix B, SHA-1, eight digits truncated to our six.
func TestTOTPVector(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	cases := map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"}
	for ts, want := range cases {
		got, err := TOTPCode(secret, time.Unix(ts, 0))
		if err != nil || got != want {
			t.Errorf("at %d: got %s (%v), want %s", ts, got, err, want)
		}
	}
}

func TestVerifyTOTPSkew(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	code, _ := TOTPCode(secret, now)
	if !VerifyTOTP(secret, code, now.Add(25*time.Second)) {
		t.Fatal("a code one step old was refused")
	}
	if VerifyTOTP(secret, code, now.Add(5*time.Minute)) {
		t.Fatal("a code five minutes old was accepted")
	}
	if VerifyTOTP(secret, "12345", now) || VerifyTOTP(secret, "", now) {
		t.Fatal("a malformed code was accepted")
	}
	uri := TOTPURI("torncity", "javad", secret)
	if !strings.HasPrefix(uri, "otpauth://totp/torncity:javad?") || !strings.Contains(uri, "secret="+secret) {
		t.Fatalf("unexpected uri %s", uri)
	}
}
