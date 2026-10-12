package switches

import (
	"context"
	"errors"
	"testing"
)

type fixedSource struct {
	val   string
	found bool
	err   error
}

func (f fixedSource) Get(context.Context, string) (string, bool, error) { return f.val, f.found, f.err }

// The mature content is off by default and "cannot tell" means off (docs/adr/0035 8, 0078 3.9): a missing reader, a switch nobody ever set and a failing read
// all answer false; only an operator's "on" answers true, and only on and off are accepted values.
func TestMatureIsOffUnlessTheOperatorTurnedItOn(t *testing.T) {
	ctx := context.Background()
	if MatureEnabled(ctx, nil) {
		t.Error("no reader means off")
	}
	if MatureEnabled(ctx, &Reader{Source: fixedSource{}}) {
		t.Error("a switch nobody set is off")
	}
	if MatureEnabled(ctx, &Reader{Source: fixedSource{err: errors.New("down")}}) {
		t.Error("a failing read is off")
	}
	if MatureEnabled(ctx, &Reader{Source: fixedSource{val: "maybe", found: true}}) {
		t.Error("only on turns it on")
	}
	if !MatureEnabled(ctx, &Reader{Source: fixedSource{val: MatureOn, found: true}}) {
		t.Error("the operator turned it on")
	}
	if !ValidMatureMode("on") || !ValidMatureMode("off") || ValidMatureMode("groups_off") || ValidMatureMode("") {
		t.Error("the values of the switch")
	}
}
