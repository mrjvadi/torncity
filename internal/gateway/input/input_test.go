package input

import "testing"

func TestEncodeDecodeRoundTrip(t *testing.T) {
	p := Pending{Command: "bank.pay", Payload: map[string]string{"to": "K7Q2M9A", "method": "card"}, Field: "amount", Prompt: 77}
	v, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	if v[:3] != "77|" {
		t.Errorf("the prompt is not the stored prefix: %q", v)
	}
	got, err := Decode(v)
	if err != nil {
		t.Fatal(err)
	}
	if got.Command != p.Command || got.Field != p.Field || got.Prompt != 77 || got.Payload["to"] != "K7Q2M9A" {
		t.Errorf("round trip lost something: %+v", got)
	}
	payload := got.Build("5000")
	if payload["amount"] != "5000" || payload["method"] != "card" || payload["to"] != "K7Q2M9A" {
		t.Errorf("payload %v", payload)
	}
	if _, err := Decode("no prefix"); err == nil {
		t.Error("a value without its prefix decoded")
	}
}

func TestParseAsk(t *testing.T) {
	c, args, ok := ParseAsk("ask:bank.pay:K7Q2M9A:card")
	if !ok || c != "bank.pay" || len(args) != 2 || args[1] != "card" {
		t.Errorf("ParseAsk = %q %v %v", c, args, ok)
	}
	for _, data := range []string{"bank:show", "ask", "ask:", "asking:bank.show"} {
		if _, _, ok := ParseAsk(data); ok {
			t.Errorf("%q parsed as a question", data)
		}
	}
}

func TestCleanValue(t *testing.T) {
	for in, want := range map[string]string{
		"  ۵۰۰۰ ": "5000",
		"٧٥٠":     "750",
		"۱۲٬۵۰۰":  "12,500",
		"12,500":  "12,500",
		"":        "",
		"   ":     "",
	} {
		if got := CleanValue(in, 0); got != want {
			t.Errorf("CleanValue(%q) = %q, want %q", in, got, want)
		}
	}
	if got := CleanValue("1234567890123", 5); got != "12345" {
		t.Errorf("the value is not capped: %q", got)
	}
}

func TestKeyIsPerBotChatAndUser(t *testing.T) {
	a := Key{BotID: "b", ChatID: -100, UserID: 7}.String()
	b := Key{BotID: "b", ChatID: -100, UserID: 8}.String()
	c := Key{BotID: "c", ChatID: -100, UserID: 7}.String()
	if a == b || a == c {
		t.Errorf("keys collide: %s %s %s", a, b, c)
	}
}
