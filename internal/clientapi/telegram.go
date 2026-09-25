package clientapi

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Telegram Mini App sign-in: the launch data Telegram signs for the Mini App
// (Telegram.WebApp.initData), checked as
// https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
// says:
//
//	data_check_string = every received field but hash, sorted by key,
//	                    "key=value" joined by "\n"
//	secret_key        = HMAC_SHA256(key="WebAppData", msg=bot_token)
//	valid             = hex(HMAC_SHA256(key=secret_key, msg=data_check_string)) == hash
//
// and, for data carrying the newer "signature" field, the third-party check
// with Telegram's Ed25519 public key over
// "<bot_id>:WebAppData\n" + the same string without hash and signature.
//
// The game runs several bots and a Mini App can be opened from any of them,
// so the data is checked against every active bot; the one it verifies for
// is the bot the player is signed in through. auth_date bounds how old the
// data may be, and the caller refuses the same data twice.

// TelegramPublicKey is Telegram's production Ed25519 key for third-party
// validation, from the page above.
const TelegramPublicKey = "e7bf03a2fa4602af4580703d88dda5bb59f32ed8b02a56c187fe7d34caed242d"

// BotCredential is one bot a Mini App may be opened from.
type BotCredential struct {
	// ID is telegram_bots.id; TelegramBotID the bot's numeric Telegram id,
	// what the Ed25519 check names; Token its Bot API token.
	ID            string
	TelegramBotID int64
	Token         string
}

// TelegramUser is the user the launch data names.
type TelegramUser struct {
	ID           int64  `json:"id"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
	IsBot        bool   `json:"is_bot"`
}

// DisplayName is the user's name as the gateway records it.
func (u TelegramUser) DisplayName() string {
	return strings.TrimSpace(strings.TrimSpace(u.FirstName) + " " + strings.TrimSpace(u.LastName))
}

// InitData is launch data that verified.
type InitData struct {
	User     TelegramUser
	Bot      BotCredential
	AuthDate time.Time
	// Hash is the data's hash field (or its signature when only that
	// verified): what a replay is recognised by.
	Hash string
}

// Mini App refusals.
var (
	ErrInitDataMalformed = errors.New("clientapi: the Mini App data is not readable")
	ErrInitDataSignature = errors.New("clientapi: the Mini App data is not signed by any of our bots")
	ErrInitDataStale     = errors.New("clientapi: the Mini App data is too old")
	ErrInitDataNoUser    = errors.New("clientapi: the Mini App data names no user")
)

// clockSkew is how far in the future an auth_date may be: the two clocks
// are not the same clock.
const clockSkew = time.Minute

// ValidateInitData checks raw launch data against bots, at now, allowing it
// to be maxAge old. publicKey is Telegram's Ed25519 key (hex); empty skips
// the third-party check.
func ValidateInitData(raw string, bots []BotCredential, maxAge time.Duration, now time.Time, publicKey string) (InitData, error) {
	values, err := url.ParseQuery(strings.TrimSpace(raw))
	if err != nil {
		return InitData{}, ErrInitDataMalformed
	}
	fields := map[string]string{}
	for k, v := range values {
		if len(v) != 1 {
			return InitData{}, ErrInitDataMalformed
		}
		fields[k] = v[0]
	}
	hash := fields["hash"]
	signature := fields["signature"]
	if hash == "" && signature == "" {
		return InitData{}, ErrInitDataMalformed
	}

	bot, ok := verifyHMAC(fields, hash, bots)
	replayKey := hash
	if !ok && signature != "" && publicKey != "" {
		bot, ok = verifySignature(fields, signature, bots, publicKey)
		replayKey = signature
	}
	if !ok {
		return InitData{}, ErrInitDataSignature
	}

	seconds, err := strconv.ParseInt(fields["auth_date"], 10, 64)
	if err != nil || seconds <= 0 {
		return InitData{}, ErrInitDataMalformed
	}
	authDate := time.Unix(seconds, 0)
	if now.Sub(authDate) > maxAge || authDate.Sub(now) > clockSkew {
		return InitData{}, ErrInitDataStale
	}

	var user TelegramUser
	if fields["user"] == "" || json.Unmarshal([]byte(fields["user"]), &user) != nil || user.ID <= 0 || user.IsBot {
		return InitData{}, ErrInitDataNoUser
	}
	return InitData{User: user, Bot: bot, AuthDate: authDate, Hash: replayKey}, nil
}

// dataCheckString joins the fields but the left-out ones, sorted.
func dataCheckString(fields map[string]string, leaveOut ...string) string {
	keys := make([]string, 0, len(fields))
next:
	for k := range fields {
		for _, l := range leaveOut {
			if k == l {
				continue next
			}
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = k + "=" + fields[k]
	}
	return strings.Join(lines, "\n")
}

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

// verifyHMAC finds the bot whose token signed the data. The signature field,
// when there is one, is a received field like any other; data signed before
// it existed, or by a client that strips it, is also tried without it.
func verifyHMAC(fields map[string]string, hash string, bots []BotCredential) (BotCredential, bool) {
	want, err := hex.DecodeString(hash)
	if err != nil || len(want) != sha256.Size {
		return BotCredential{}, false
	}
	candidates := []string{dataCheckString(fields, "hash")}
	if _, ok := fields["signature"]; ok {
		candidates = append(candidates, dataCheckString(fields, "hash", "signature"))
	}
	var found BotCredential
	matched := false
	for _, bot := range bots {
		if bot.Token == "" {
			continue
		}
		secret := hmacSHA256([]byte("WebAppData"), []byte(bot.Token))
		for _, dcs := range candidates {
			// Every bot is checked, whatever matched before, so how long
			// this takes says nothing about which bot it was.
			if hmac.Equal(hmacSHA256(secret, []byte(dcs)), want) && !matched {
				found, matched = bot, true
			}
		}
	}
	return found, matched
}

// verifySignature is the third-party check with Telegram's public key.
func verifySignature(fields map[string]string, signature string, bots []BotCredential, publicKey string) (BotCredential, bool) {
	key, err := hex.DecodeString(publicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return BotCredential{}, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(signature, "="))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return BotCredential{}, false
	}
	dcs := dataCheckString(fields, "hash", "signature")
	for _, bot := range bots {
		if bot.TelegramBotID == 0 {
			continue
		}
		msg := strconv.FormatInt(bot.TelegramBotID, 10) + ":WebAppData\n" + dcs
		if ed25519.Verify(key, []byte(msg), sig) {
			return bot, true
		}
	}
	return BotCredential{}, false
}
