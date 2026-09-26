package clientapi

import "testing"

func shippedActionMetadata(t *testing.T) *ActionMetadata {
	t.Helper()
	m, err := LoadActionMetadata("../../configs/actions.yml")
	if err != nil {
		t.Fatalf("configs/actions.yml does not load: %v", err)
	}
	return m
}

// Every command the game serves to players must have a line in
// configs/actions.yml: a client is never left to guess a kind or an icon.
func TestShippedActionMetadataCoversEveryCommand(t *testing.T) {
	m := shippedActionMetadata(t)
	if missing := m.Missing(); len(missing) > 0 {
		t.Errorf("configs/actions.yml has no line for %v", missing)
	}
}

func TestActionMetadataOf(t *testing.T) {
	m := shippedActionMetadata(t)
	if got := m.Of("bank.deposit"); got.Kind != KindPrimary || got.Icon != "action:deposit" {
		t.Errorf("bank.deposit = %+v", got)
	}
	if got := m.Of("job.quit"); got.Kind != KindDanger {
		t.Errorf("job.quit = %+v", got)
	}
	if got := m.Of("crime.list"); got.Kind != KindNavigation {
		t.Errorf("crime.list = %+v", got)
	}
	// A command that does not exist gets the file's defaults, not a panic.
	if got := m.Of("nope.nope"); got.Kind != KindSecondary || got.Icon != "action:default" {
		t.Errorf("unknown command = %+v", got)
	}
	// A nil table is just as safe: every client answer still carries a kind
	// and an icon.
	var nilMeta *ActionMetadata
	if got := nilMeta.Of("bank.deposit"); got.Kind != KindSecondary || got.Icon != "action:default" {
		t.Errorf("nil table = %+v", got)
	}
}

func TestParseActionMetadataRejectsBadLines(t *testing.T) {
	cases := []string{
		"defaults: {kind: secondary, icon: \"action:default\"}\ncommands:\n  nope.nope: {kind: primary, icon: \"x\"}\n",
		"defaults: {kind: secondary, icon: \"action:default\"}\ncommands:\n  bank.deposit: {kind: not_a_kind, icon: \"x\"}\n",
		"defaults: {kind: secondary, icon: \"action:default\"}\ncommands:\n  bank.deposit: {kind: primary}\n",
		"defaults: {kind: not_a_kind, icon: \"action:default\"}\ncommands: {}\n",
		"not: [valid\n",
	}
	for i, c := range cases {
		if _, err := ParseActionMetadata([]byte(c)); err == nil {
			t.Errorf("case %d: accepted", i)
		}
	}
}
