package groups

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/gateway/routing"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

var (
	jsonMarshal   = json.Marshal
	jsonUnmarshal = json.Unmarshal
)

func TestIsGroupChat(t *testing.T) {
	for _, tc := range []struct {
		chatType string
		id       int64
		want     bool
	}{
		{"private", 42, false},
		{"group", -42, true},
		{"supergroup", -10042, true},
		{"", -10042, true}, // a lost chat type is still a room
		{"", 42, false},
	} {
		if got := IsGroupChat(tc.chatType, tc.id); got != tc.want {
			t.Errorf("IsGroupChat(%q, %d) = %v", tc.chatType, tc.id, got)
		}
	}
}

func TestCommandAddressee(t *testing.T) {
	for _, tc := range []struct {
		text, name string
		isCommand  bool
	}{
		{"/map", "", true},
		{"/map@torn_bot", "torn_bot", true},
		{"/social@Torn_Bot mrjvadi", "Torn_Bot", true},
		{"hello @torn_bot", "", false},
		{"", "", false},
	} {
		name, isCommand := CommandAddressee(tc.text)
		if name != tc.name || isCommand != tc.isCommand {
			t.Errorf("CommandAddressee(%q) = %q, %v", tc.text, name, isCommand)
		}
	}
	if !SameBot("Torn_Bot", "@torn_bot") || SameBot("other_bot", "torn_bot") {
		t.Error("SameBot is wrong")
	}
}

func TestOwnerTagRoundTrip(t *testing.T) {
	const owner = int64(8_123_456_789)
	bound, ok := BindOwner("player:language.set:en", owner)
	if !ok {
		t.Fatal("not bound")
	}
	if len(bound) > MaxCallbackDataBytes {
		t.Fatalf("bound datum %q is %d bytes", bound, len(bound))
	}
	got, rest, isBound := SplitOwner(bound)
	if !isBound || got != owner || rest != "player:language.set:en" {
		t.Fatalf("SplitOwner(%q) = %d %q %v", bound, got, rest, isBound)
	}
	// The tagged datum is still well-formed callback data once split, and
	// the untagged form routes exactly as before.
	if _, _, err := routing.ParseCallbackData(rest); err != nil {
		t.Fatal(err)
	}
	if _, rest, isBound := SplitOwner("map:list"); isBound || rest != "map:list" {
		t.Error("untagged data was read as tagged")
	}
}

// A datum that would exceed Telegram's 64 bytes once tagged stays untagged,
// rather than becoming a button Telegram refuses.
func TestOwnerTagNeverBreaksTheLimit(t *testing.T) {
	long := "social:friend.accept:" + strings.Repeat("a", 64-len("social:friend.accept:"))
	got, ok := BindOwner(long, 8_123_456_789)
	if ok || got != long {
		t.Fatalf("BindOwner of a full datum = %q, %v", got, ok)
	}
	for _, data := range []string{"", "-abc:map:list"} {
		if _, ok := BindOwner(data, 1); ok {
			t.Errorf("BindOwner(%q) bound", data)
		}
	}
	if _, ok := BindOwner("map:list", 0); ok {
		t.Error("bound to nobody")
	}
}

func TestMalformedTagIsBoundToNobody(t *testing.T) {
	for _, data := range []string{"-", "-zz", "-!!:map:list", "-0:map:list"} {
		owner, _, bound := SplitOwner(data)
		if !bound || owner != 0 {
			t.Errorf("SplitOwner(%q) = %d, %v; want refused", data, owner, bound)
		}
	}
}

func TestBindKeyboardCopies(t *testing.T) {
	kb := &presenter.Keyboard{Rows: [][]presenter.Button{{
		{Text: "a", CallbackData: "map:list"},
		{Text: "b", URL: "https://t.me/x"},
	}}}
	out := BindKeyboard(kb, 99)
	if kb.Rows[0][0].CallbackData != "map:list" {
		t.Fatal("the original keyboard was modified")
	}
	if !strings.HasPrefix(out.Rows[0][0].CallbackData, ownerMark) || out.Rows[0][1].URL != "https://t.me/x" {
		t.Errorf("bound keyboard = %+v", out)
	}
	if BindKeyboard(nil, 1) != nil {
		t.Error("nil keyboard grew buttons")
	}
}

// Every command a player can send survives the deep-link round trip within
// Telegram's payload rules.
func TestStartPayloadRoundTripsEveryPlayerCommand(t *testing.T) {
	for _, s := range commands.All() {
		if s.Origin != commands.FromPlayer {
			continue
		}
		payload := StartPayload(s.Command())
		if payload == "" || len(payload) > maxStartPayload {
			t.Errorf("%s: payload %q", s.Command(), payload)
			continue
		}
		got, args, ok := CommandFromStart("/start " + payload)
		if !ok || got != s.Command() || len(args) != 0 {
			t.Errorf("%s: round trip gave %q %v, %v", s.Command(), got, args, ok)
		}
	}
	for _, text := range []string{"/start", "/start K7Q2M9A", "/start run-", "/start run-Map-list", "/profile run-map-list",
		"/start run-map-list extra", "/start run---K7Q2M9A", "/start run-bank-pay--", "/start run-bank-pay--K7Q2M9A--5000"} {
		if cmd, args, ok := CommandFromStart(text); ok {
			t.Errorf("CommandFromStart(%q) = %q %v", text, cmd, args)
		}
	}
	if DeepLink("", "run-map-list") != "" || DeepLink("@torn_bot", "") != "https://t.me/torn_bot" {
		t.Error("DeepLink is wrong")
	}
}

func TestMembershipChanges(t *testing.T) {
	if !Joined("left", "member") || !Joined("kicked", "administrator") || Joined("member", "administrator") {
		t.Error("Joined is wrong")
	}
	if !Left("member", "left") || !Left("administrator", "kicked") || Left("left", "kicked") {
		t.Error("Left is wrong")
	}
}

// memoryStore is a set-if-absent store shared by several bots, as Redis is.
type memoryStore struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (m *memoryStore) Seen(_ context.Context, key string, id int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key + "#" + strconv.FormatInt(id, 10)
	if m.seen[k] {
		return true, nil
	}
	m.seen[k] = true
	return false, nil
}

// Two of our bots receive the same unaddressed command, concurrently, each
// with its own message id (as in a basic group): exactly one claims it.
func TestExactlyOneBotClaimsAGroupCommand(t *testing.T) {
	store := &memoryStore{seen: map[string]bool{}}
	claimer := NewClaimer(store)

	msgFor := func(messageID int64) *client.Message {
		return &client.Message{
			MessageID: messageID,
			From:      &client.User{ID: 5},
			Chat:      client.Chat{ID: -300, Type: "group"},
			Date:      1_700_000_000,
			Text:      "/map",
		}
	}
	var wins int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := int64(1); i <= 5; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			won, err := claimer.Claim(context.Background(), msgFor(id))
			if err != nil {
				t.Error(err)
			}
			if won {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d bots answered, want exactly one", wins)
	}

	// A different message (another text) is claimed afresh.
	other := msgFor(9)
	other.Text = "/skills"
	if won, _ := claimer.Claim(context.Background(), other); !won {
		t.Error("a new command was not claimable")
	}
}
