package clientapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"testing"
	"time"
)

var testBots = []BotCredential{
	{ID: "bot-a", TelegramBotID: 1001, Token: "1001:AAA-token-a"},
	{ID: "bot-b", TelegramBotID: 1002, Token: "1002:BBB-token-b"},
	{ID: "bot-c", TelegramBotID: 1003, Token: "1003:CCC-token-c"},
}

const testUser = `{"id":424242,"first_name":"Sara","last_name":"K","username":"sara","language_code":"fa"}`

// signInitData builds launch data the way Telegram does for token.
func signInitData(token string, authDate time.Time, extra map[string]string) string {
	fields := map[string]string{
		"auth_date": strconv.FormatInt(authDate.Unix(), 10),
		"query_id":  "AAHdF6IQAAAAAN0XohDhrOrc",
		"user":      testUser,
	}
	for k, v := range extra {
		fields[k] = v
	}
	secret := hmacSHA256([]byte("WebAppData"), []byte(token))
	fields["hash"] = hex.EncodeToString(hmacSHA256(secret, []byte(dataCheckString(fields))))
	q := url.Values{}
	for k, v := range fields {
		q.Set(k, v)
	}
	return q.Encode()
}

func TestInitDataVerifiesAgainstTheBotThatSignedIt(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, bot := range testBots {
		got, err := ValidateInitData(signInitData(bot.Token, now.Add(-time.Minute), nil), testBots, time.Hour, now, "")
		if err != nil {
			t.Fatalf("%s: %v", bot.ID, err)
		}
		if got.Bot.ID != bot.ID || got.User.ID != 424242 || got.User.Username != "sara" || got.User.LanguageCode != "fa" || got.Hash == "" {
			t.Errorf("%s: got %+v", bot.ID, got)
		}
	}
}

func TestInitDataRefusals(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	good := signInitData(testBots[1].Token, now.Add(-time.Minute), nil)

	cases := map[string]struct {
		raw  string
		bots []BotCredential
		want error
	}{
		"another bot's token": {signInitData("9999:ZZZ", now, nil), testBots, ErrInitDataSignature},
		"no bot of ours":      {good, testBots[:1], ErrInitDataSignature},
		"stale":               {signInitData(testBots[0].Token, now.Add(-2*time.Hour), nil), testBots, ErrInitDataStale},
		"from the future":     {signInitData(testBots[0].Token, now.Add(time.Hour), nil), testBots, ErrInitDataStale},
		"no hash":             {"auth_date=1&user=%7B%7D", testBots, ErrInitDataMalformed},
	}
	tampered, _ := url.ParseQuery(good)
	tampered.Set("user", `{"id":1,"first_name":"Mallory"}`)
	cases["tampered user"] = struct {
		raw  string
		bots []BotCredential
		want error
	}{tampered.Encode(), testBots, ErrInitDataSignature}

	for name, c := range cases {
		if _, err := ValidateInitData(c.raw, c.bots, time.Hour, now, ""); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
}

func TestInitDataWithSignatureField(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	fields := map[string]string{
		"auth_date": strconv.FormatInt(now.Unix(), 10),
		"user":      testUser,
	}
	msg := "1003:WebAppData\n" + dataCheckString(fields)
	sig := base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(msg)))

	// Signed with the bot token too, with the signature among the fields:
	// the HMAC check alone accepts it.
	withHash := signInitData(testBots[2].Token, now, map[string]string{"signature": sig})
	got, err := ValidateInitData(withHash, testBots, time.Hour, now, "")
	if err != nil || got.Bot.ID != "bot-c" {
		t.Fatalf("hmac with a signature field: %+v %v", got, err)
	}

	// No usable hash: only the public-key check can accept it.
	q := url.Values{"auth_date": {fields["auth_date"]}, "user": {testUser}, "signature": {sig}, "hash": {"00"}}
	got, err = ValidateInitData(q.Encode(), testBots, time.Hour, now, hex.EncodeToString(pub))
	if err != nil || got.Bot.ID != "bot-c" || got.Hash != sig {
		t.Fatalf("signature: %+v %v", got, err)
	}
	if _, err := ValidateInitData(q.Encode(), testBots, time.Hour, now, TelegramPublicKey); !errors.Is(err, ErrInitDataSignature) {
		t.Errorf("a signature under another key passed: %v", err)
	}
}
