package registry

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
)

// The values below are placeholders shaped like tokens so that a leak would be
// obvious in a diff. They authenticate nothing.
const (
	tokenBot01 = "000000001:PLACEHOLDER_NOT_A_REAL_TOKEN_BOT01"
	tokenBot02 = "000000002:PLACEHOLDER_NOT_A_REAL_TOKEN_BOT02"
	tokenBot09 = "000000009:PLACEHOLDER_NOT_A_REAL_TOKEN_BOT09"
)

// fakeSource is application.BotRegistry backed by a slice, plus a switch for
// making the next call fail.
type fakeSource struct {
	bots  []application.Bot
	err   error
	calls int
}

func (f *fakeSource) ListEnabled(ctx context.Context) ([]application.Bot, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make([]application.Bot, len(f.bots))
	copy(out, f.bots)
	return out, nil
}

// fakeSecrets is application.SecretResolver backed by a map.
type fakeSecrets struct {
	values map[string]string
	err    error
}

func (f *fakeSecrets) Resolve(ref string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	value, ok := f.values[ref]
	if !ok {
		// Mirrors the real contract: the failure names the ref, never a value.
		return "", fmt.Errorf("fake secrets: no secret named %q", ref)
	}
	return value, nil
}

func fleet() ([]application.Bot, *fakeSecrets) {
	bots := []application.Bot{
		{
			ID: "b-2", BotKey: "bot02", TelegramBotID: 2, Username: "torn_two",
			TokenSecretRef: "telegram/bot02", Status: "active", GatewayGroup: "a",
			Enabled: true, RateLimit: 20,
		},
		{
			ID: "b-1", BotKey: "bot01", TelegramBotID: 1, Username: "torn_one",
			TokenSecretRef: "telegram/bot01", Status: "active", GatewayGroup: "a",
			Enabled: true, RateLimit: 25,
		},
		{
			// A disabled row that the source hands over anyway. The registry
			// must drop it rather than trust the caller.
			ID: "b-9", BotKey: "bot09", TelegramBotID: 9, Username: "torn_retired",
			TokenSecretRef: "telegram/bot09", Status: "disabled", GatewayGroup: "a",
			Enabled: false, RateLimit: 25,
		},
	}
	secrets := &fakeSecrets{values: map[string]string{
		"telegram/bot01": tokenBot01,
		"telegram/bot02": tokenBot02,
		"telegram/bot09": tokenBot09,
	}}
	return bots, secrets
}

func newTestRegistry(t *testing.T) (*Registry, *fakeSource, *fakeSecrets) {
	t.Helper()

	bots, secrets := fleet()
	source := &fakeSource{bots: bots}
	reg, err := New(Config{Source: source, Secrets: secrets, BaseURL: "http://telegram-bot-api:8081"})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	if err := reg.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh returned an error: %v", err)
	}
	return reg, source, secrets
}

func TestRefreshLoadsTheEnabledFleet(t *testing.T) {
	reg, _, _ := newTestRegistry(t)

	if got := reg.Len(); got != 2 {
		t.Errorf("Len = %d, want 2", got)
	}

	bot, ok := reg.Get("bot01")
	if !ok {
		t.Fatal("Get(bot01) reports the bot is not loaded")
	}
	if bot.Username != "torn_one" || bot.RateLimit != 25 {
		t.Errorf("Get(bot01) = %+v, want the row from the source", bot)
	}

	if _, ok := reg.Get("bot09"); ok {
		t.Error("a disabled bot is present in the registry")
	}
	if _, err := reg.ClientFor("bot09"); !errors.Is(err, ErrUnknownBot) {
		t.Errorf("ClientFor(bot09) error = %v, want %v", err, ErrUnknownBot)
	}

	// All is ordered by bot key so two gateway instances walk the fleet the
	// same way.
	all := reg.All()
	if len(all) != 2 || all[0].BotKey != "bot01" || all[1].BotKey != "bot02" {
		t.Errorf("All = %+v, want bot01 then bot02", all)
	}
	if keys := reg.Keys(); !reflect.DeepEqual(keys, []string{"bot01", "bot02"}) {
		t.Errorf("Keys = %v, want [bot01 bot02]", keys)
	}
}

// TestNoTokenIsExposed is the security test of this package. A token that has
// reached a field, a return value or a formatted string has already failed
// ADR 0002, whether or not anyone logs it today.
func TestNoTokenIsExposed(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	tokens := []string{tokenBot01, tokenBot02, tokenBot09}

	bot01Client, err := reg.ClientFor("bot01")
	if err != nil {
		t.Fatalf("ClientFor(bot01) returned an error: %v", err)
	}

	rendered := map[string]string{
		"%+v of the registry":   fmt.Sprintf("%+v", reg),
		"%#v of the registry":   fmt.Sprintf("%#v", reg),
		"%v of the registry":    fmt.Sprintf("%v", reg),
		"%+v of All()":          fmt.Sprintf("%+v", reg.All()),
		"%+v of a Get() row":    fmt.Sprintf("%+v", mustGet(t, reg, "bot01")),
		"%+v of ClientFor()":    fmt.Sprintf("%+v", bot01Client),
		"%s of ClientFor()":     fmt.Sprintf("%s", bot01Client),
		"the ClientFor BaseURL": bot01Client.BaseURL(),
	}
	for what, text := range rendered {
		for _, token := range tokens {
			if strings.Contains(text, token) {
				t.Errorf("%s leaked a bot token: %s", what, text)
			}
		}
	}

	// Formatting only proves that today's fmt verbs are safe. Walking the
	// registry's fields with reflection proves the token is not stored
	// anywhere at all, which is the property that survives a refactor.
	for _, field := range reachableStrings(t, reg) {
		for _, token := range tokens {
			if strings.Contains(field, token) {
				t.Errorf("a field reachable from Registry holds a bot token: %q", field)
			}
		}
	}
}

// TestTheSecretRefIsNotTheSecret pins the distinction the whole scheme rests
// on: the row carries a name, and the name is safe to pass around.
func TestTheSecretRefIsNotTheSecret(t *testing.T) {
	reg, _, _ := newTestRegistry(t)

	bot := mustGet(t, reg, "bot01")
	if bot.TokenSecretRef != "telegram/bot01" {
		t.Errorf("TokenSecretRef = %q, want the name of the secret", bot.TokenSecretRef)
	}
	if strings.Contains(bot.TokenSecretRef, "PLACEHOLDER") {
		t.Error("TokenSecretRef holds the resolved secret instead of its name")
	}
}

func TestClientsArePerBot(t *testing.T) {
	reg, _, _ := newTestRegistry(t)

	first, err := reg.ClientFor("bot01")
	if err != nil {
		t.Fatalf("ClientFor(bot01): %v", err)
	}
	second, err := reg.ClientFor("bot02")
	if err != nil {
		t.Fatalf("ClientFor(bot02): %v", err)
	}
	if first == second {
		t.Error("two bots share one API client, so one token would be used for both")
	}
	if first.BaseURL() != "http://telegram-bot-api:8081" {
		t.Errorf("BaseURL = %q, want the configured local Bot API server", first.BaseURL())
	}

	// The same bot must keep the same client across calls: a client rebuilt
	// per call would throw away the connection pool to the local server.
	again, err := reg.ClientFor("bot01")
	if err != nil {
		t.Fatalf("ClientFor(bot01) again: %v", err)
	}
	if again != first {
		t.Error("ClientFor returned a different client for the same bot")
	}
}

// TestRefreshIsAtomic covers the failure modes an operator will actually hit:
// the database is down, or one secret is missing. Neither may empty the fleet.
func TestRefreshIsAtomic(t *testing.T) {
	tests := []struct {
		name     string
		sabotage func(*fakeSource, *fakeSecrets)
	}{
		{
			name:     "the source fails",
			sabotage: func(s *fakeSource, _ *fakeSecrets) { s.err = errors.New("db: connection refused") },
		},
		{
			name: "a secret cannot be resolved",
			sabotage: func(s *fakeSource, _ *fakeSecrets) {
				// A new bot whose secret ref names nothing the resolver knows.
				s.bots = append(s.bots, application.Bot{
					ID: "b-3", BotKey: "bot03", TokenSecretRef: "telegram/bot03", Enabled: true,
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, source, secrets := newTestRegistry(t)
			tt.sabotage(source, secrets)

			if err := reg.Refresh(context.Background()); err == nil {
				t.Fatal("Refresh succeeded, want an error")
			}
			if got := reg.Len(); got != 2 {
				t.Errorf("Len = %d after a failed refresh, want the previous snapshot of 2", got)
			}
			if _, err := reg.ClientFor("bot01"); err != nil {
				t.Errorf("bot01 stopped working after a failed refresh: %v", err)
			}
		})
	}
}

// TestFailedRefreshErrorDoesNotLeakTheSecret: an error text travels to a log,
// which is exactly where a token may not go.
func TestFailedRefreshErrorDoesNotLeakTheSecret(t *testing.T) {
	reg, source, secrets := newTestRegistry(t)
	secrets.err = errors.New("vault: sealed")
	source.bots = append(source.bots, application.Bot{
		ID: "b-3", BotKey: "bot03", TokenSecretRef: "telegram/bot03", Enabled: true,
	})

	err := reg.Refresh(context.Background())
	if err == nil {
		t.Fatal("Refresh succeeded, want an error")
	}
	for _, token := range []string{tokenBot01, tokenBot02} {
		if strings.Contains(err.Error(), token) {
			t.Errorf("the refresh error leaked a token: %v", err)
		}
	}
	if !strings.Contains(err.Error(), "bot0") {
		t.Errorf("the refresh error does not say which bot failed: %v", err)
	}
}

func TestRefreshRejectsADuplicateBotKey(t *testing.T) {
	bots, secrets := fleet()
	bots = append(bots, application.Bot{
		ID: "b-1-again", BotKey: "bot01", TokenSecretRef: "telegram/bot01", Enabled: true,
	})
	reg, err := New(Config{Source: &fakeSource{bots: bots}, Secrets: secrets})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := reg.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh accepted a duplicate bot_key; one of the two bots would be invisible")
	}
}

func TestNewRequiresItsPorts(t *testing.T) {
	bots, secrets := fleet()

	if _, err := New(Config{Secrets: secrets}); err != ErrNoSource {
		t.Errorf("New without a source: %v, want %v", err, ErrNoSource)
	}
	if _, err := New(Config{Source: &fakeSource{bots: bots}}); err != ErrNoSecrets {
		t.Errorf("New without a secret resolver: %v, want %v", err, ErrNoSecrets)
	}
}

func TestAnEmptyRegistryAnswersCleanly(t *testing.T) {
	reg, err := New(Config{Source: &fakeSource{}, Secrets: &fakeSecrets{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if reg.Len() != 0 || len(reg.All()) != 0 || len(reg.Keys()) != 0 {
		t.Error("a registry that has never refreshed reports bots")
	}
	if _, ok := reg.Get("bot01"); ok {
		t.Error("Get found a bot in an empty registry")
	}
	if _, err := reg.ClientFor("bot01"); !errors.Is(err, ErrUnknownBot) {
		t.Errorf("ClientFor error = %v, want %v", err, ErrUnknownBot)
	}
}

func mustGet(t *testing.T, reg *Registry, botKey string) application.Bot {
	t.Helper()

	bot, ok := reg.Get(botKey)
	if !ok {
		t.Fatalf("Get(%q) reports the bot is not loaded", botKey)
	}
	return bot
}

// reachableStrings collects every string value reachable from v, including the
// ones in unexported fields, which is where a leaked credential would sit.
//
// Two things are deliberately not walked:
//
//   - *client.Client, because the token does live there. That is the one place
//     it is allowed to be: an unexported field of a package whose own tests
//     prove it never prints it.
//   - the source and secrets ports, because a secret resolver's whole job is
//     to hold secrets. Walking into it would test the fake, not the registry.
func reachableStrings(t *testing.T, reg *Registry) []string {
	t.Helper()

	var found []string
	collectStrings(reflect.ValueOf(reg), &found, 0)
	return found
}

var clientType = reflect.TypeOf((*client.Client)(nil))

// skippedFields are the Registry fields whose contents belong to another
// package; see reachableStrings.
var skippedFields = map[string]bool{"source": true, "secrets": true}

func collectStrings(v reflect.Value, out *[]string, depth int) {
	if !v.IsValid() || depth > 10 {
		return
	}

	switch v.Kind() {
	case reflect.String:
		*out = append(*out, v.String())
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() || (v.Kind() == reflect.Pointer && v.Type() == clientType) {
			return
		}
		collectStrings(v.Elem(), out, depth+1)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if skippedFields[v.Type().Field(i).Name] {
				continue
			}
			collectStrings(v.Field(i), out, depth+1)
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			collectStrings(iter.Key(), out, depth+1)
			collectStrings(iter.Value(), out, depth+1)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			collectStrings(v.Index(i), out, depth+1)
		}
	}
}
