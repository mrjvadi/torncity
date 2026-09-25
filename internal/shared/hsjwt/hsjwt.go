// Package hsjwt signs and verifies JSON Web Tokens with HMAC-SHA256 (RFC
// 7519, alg HS256), the one algorithm this project issues: the client API's
// access tokens and the realtime server's connection and subscription
// tokens.
//
// It is deliberately small. Verify accepts exactly the header this package
// writes ({"alg":"HS256","typ":"JWT"}, in any key order) and nothing else,
// so an "alg":"none" token or one signed with a public key as an HMAC secret
// is refused before its signature is looked at.
package hsjwt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Failures.
var (
	ErrMalformed = errors.New("hsjwt: the token is not a JWT")
	ErrAlgorithm = errors.New("hsjwt: the token is not signed with HS256")
	ErrSignature = errors.New("hsjwt: the signature does not match")
	ErrExpired   = errors.New("hsjwt: the token has expired")
	ErrNoSecret  = errors.New("hsjwt: no signing secret")
)

var enc = base64.RawURLEncoding

const header = `{"alg":"HS256","typ":"JWT"}`

// Sign encodes claims and signs them with secret.
func Sign(secret []byte, claims any) (string, error) {
	if len(secret) == 0 {
		return "", ErrNoSecret
	}
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := enc.EncodeToString([]byte(header)) + "." + enc.EncodeToString(body)
	return unsigned + "." + enc.EncodeToString(mac(secret, unsigned)), nil
}

// Verify checks the token's header and signature and decodes its claims into
// dst. It does not look at the claims: expiry and audience are the caller's
// (see Registered.Check).
func Verify(secret []byte, token string, dst any) error {
	if len(secret) == 0 {
		return ErrNoSecret
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ErrMalformed
	}
	rawHeader, err := enc.DecodeString(parts[0])
	if err != nil {
		return ErrMalformed
	}
	var h struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(rawHeader, &h); err != nil {
		return ErrMalformed
	}
	if h.Alg != "HS256" || (h.Typ != "" && h.Typ != "JWT") {
		return ErrAlgorithm
	}
	sig, err := enc.DecodeString(parts[2])
	if err != nil {
		return ErrMalformed
	}
	if !hmac.Equal(sig, mac(secret, parts[0]+"."+parts[1])) {
		return ErrSignature
	}
	body, err := enc.DecodeString(parts[1])
	if err != nil {
		return ErrMalformed
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return ErrMalformed
	}
	return nil
}

func mac(secret []byte, s string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(s))
	return m.Sum(nil)
}

// Registered are the registered claims this project uses.
type Registered struct {
	Subject   string `json:"sub"`
	ExpiresAt int64  `json:"exp,omitempty"`
	IssuedAt  int64  `json:"iat,omitempty"`
	ID        string `json:"jti,omitempty"`
	Audience  string `json:"aud,omitempty"`
	Issuer    string `json:"iss,omitempty"`
}

// Check reports whether the claims are live at now and meant for audience
// (when audience is not empty).
func (r Registered) Check(now time.Time, audience string) error {
	if r.ExpiresAt == 0 || now.Unix() >= r.ExpiresAt {
		return ErrExpired
	}
	if audience != "" && r.Audience != audience {
		return ErrMalformed
	}
	return nil
}
