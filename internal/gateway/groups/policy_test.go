package groups

import (
	"errors"
	"testing"

	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

func shippedPolicy(t *testing.T) *Policy {
	t.Helper()
	p, err := LoadPolicy("../../../configs/commands.yml")
	if err != nil {
		t.Fatalf("configs/commands.yml does not load: %v", err)
	}
	return p
}

// Every command the game serves to players has a line: where it may run is
// decided, never defaulted.
func TestEveryServedCommandHasAChannel(t *testing.T) {
	p := shippedPolicy(t)
	if missing := p.Missing(); len(missing) > 0 {
		t.Errorf("configs/commands.yml has no line for %v", missing)
	}
}

// The owner's rules, as written.
func TestOwnerChannelRules(t *testing.T) {
	p := shippedPolicy(t)
	for command, want := range map[string]Channel{
		"crime.commit":  ChannelGroup,
		"crime.hub":     ChannelGroup,
		"bank.show":     ChannelPrivate,
		"bank.deposit":  ChannelPrivate,
		"bank.withdraw": ChannelPrivate,
		"bank.pay":      ChannelBoth,
		"travel.start":  ChannelPrivate,
	} {
		if got := p.Channel(command); got != want {
			t.Errorf("%s runs in %q, want %q", command, got, want)
		}
	}
	if p.Allowed("crime.commit", false) || !p.Allowed("crime.commit", true) {
		t.Error("crime is not group-only")
	}
	if p.Allowed("bank.show", true) || !p.Allowed("bank.show", false) {
		t.Error("the bank is not private-only")
	}
	// A payment started in a group still confirms and receipts privately.
	if !p.IsPrivate("bank.pay", presenter.Message("x", nil)) {
		t.Error("a payment's reply would be posted in the group")
	}
	if p.IsPrivate("player.profile.get", presenter.Message("x", nil)) {
		t.Error("the profile would never be shown in a group")
	}
	if !p.IsPrivate("player.profile.get", presenter.Message("x", nil).MarkPrivate()) {
		t.Error("a screen marked private is posted anyway")
	}
}

// A line for an unserved command, or a channel that does not exist, is
// refused when the table is read.
func TestPolicyRejectsBadLines(t *testing.T) {
	if _, err := ParsePolicy([]byte("commands:\n  casino.spin: {channel: group}\n")); !errors.Is(err, ErrPolicyCommand) {
		t.Errorf("an unserved command loaded: %v", err)
	}
	if _, err := ParsePolicy([]byte("commands:\n  crime.hub: {channel: everywhere}\n")); !errors.Is(err, ErrPolicyChannel) {
		t.Errorf("a bad channel loaded: %v", err)
	}
	if _, err := ParsePolicy([]byte("input:\n  bank.deposit: {}\n")); !errors.Is(err, ErrPolicyCommand) {
		t.Errorf("an input with no field loaded: %v", err)
	}
}

// With no table at all, nothing is locked out and the old privacy holds.
func TestNilPolicyAllowsEverything(t *testing.T) {
	var p *Policy
	for _, s := range commands.All() {
		if !p.Allowed(s.Command(), true) || !p.Allowed(s.Command(), false) {
			t.Errorf("%s is locked out without a table", s.Command())
		}
	}
	if !p.IsPrivate("bank.show", nil) || p.IsPrivate("map.list", nil) {
		t.Error("the legacy privacy is lost without a table")
	}
}

// The inputs the shipped table allows fill a field of a served command.
func TestShippedInputs(t *testing.T) {
	p := shippedPolicy(t)
	for _, command := range []string{"bank.deposit", "bank.withdraw", "bank.pay"} {
		spec, ok := p.Input(command)
		if !ok || spec.Field != "amount" {
			t.Errorf("%s asks for no amount: %+v", command, spec)
		}
	}
	if _, ok := p.Input("crime.commit"); ok {
		t.Error("an unlisted command may ask for input")
	}
}
