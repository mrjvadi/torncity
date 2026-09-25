package hsjwt

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

type claims struct {
	Registered
	Lang string `json:"lang"`
}

func TestSignVerifyRoundTrip(t *testing.T) {
	secret := []byte("s3cret")
	now := time.Unix(1_800_000_000, 0)
	tok, err := Sign(secret, claims{Registered{Subject: "p1", ExpiresAt: now.Add(time.Minute).Unix(), Audience: "a"}, "fa"})
	if err != nil {
		t.Fatal(err)
	}
	var got claims
	if err := Verify(secret, tok, &got); err != nil {
		t.Fatal(err)
	}
	if got.Subject != "p1" || got.Lang != "fa" {
		t.Errorf("got %+v", got)
	}
	if err := got.Check(now, "a"); err != nil {
		t.Errorf("Check: %v", err)
	}
	if err := got.Check(now.Add(time.Minute), "a"); !errors.Is(err, ErrExpired) {
		t.Errorf("an expired token passed: %v", err)
	}
	if err := got.Check(now, "b"); err == nil {
		t.Error("a token for another audience passed")
	}
}

func TestVerifyRefusesForgeries(t *testing.T) {
	secret := []byte("s3cret")
	tok, _ := Sign(secret, claims{Registered: Registered{Subject: "p1", ExpiresAt: 1 << 40}})
	parts := strings.Split(tok, ".")

	var dst claims
	if err := Verify([]byte("other"), tok, &dst); !errors.Is(err, ErrSignature) {
		t.Errorf("wrong secret: %v", err)
	}
	none := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	if err := Verify(secret, none+"."+parts[1]+".", &dst); !errors.Is(err, ErrAlgorithm) {
		t.Errorf("alg none: %v", err)
	}
	body := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"p2","exp":1099511627776}`))
	if err := Verify(secret, parts[0]+"."+body+"."+parts[2], &dst); !errors.Is(err, ErrSignature) {
		t.Errorf("tampered body: %v", err)
	}
	if err := Verify(secret, "abc", &dst); !errors.Is(err, ErrMalformed) {
		t.Errorf("garbage: %v", err)
	}
	if _, err := Sign(nil, claims{}); !errors.Is(err, ErrNoSecret) {
		t.Errorf("empty secret: %v", err)
	}
}
