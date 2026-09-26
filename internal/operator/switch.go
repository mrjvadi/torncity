package operator

import (
	"context"
	"fmt"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/switches"
)

// The operator's runtime switches (migrations/0041_runtime_switches):
// cmd/admin ("admin switch set/list") and the panel's System > Switches page
// both call these, so a switch flips in exactly one way, with one audit
// trail, whichever door it comes through.

// SetSwitch stores key's new value, audited. It refuses a value the switch
// itself does not accept, so a typo cannot silently take effect: telegram_play
// only takes "on", "groups_off" or "off", telegram_notices only "on" or
// "off"; any other key is stored as given, for whatever future switch reuses
// this table.
func (o Ops) SetSwitch(ctx context.Context, key, value string, actor Actor) (postgres.SwitchState, error) {
	actor, err := actor.check()
	if err != nil {
		return postgres.SwitchState{}, err
	}
	switch key {
	case switches.KeyTelegramPlay:
		if !switches.ValidPlayMode(value) {
			return postgres.SwitchState{}, fmt.Errorf("operator: %s takes on, groups_off or off, not %q", key, value)
		}
	case switches.KeyTelegramNotices:
		if !switches.ValidNoticeMode(value) {
			return postgres.SwitchState{}, fmt.Errorf("operator: %s takes on or off, not %q", key, value)
		}
	}
	return postgres.NewSwitchOps(o.Pool).Set(ctx, postgres.SwitchChange{Key: key, Value: value, Actor: actor.panel()})
}

// Switches lists every switch's current row.
func (o Ops) Switches(ctx context.Context) ([]postgres.SwitchState, error) {
	return postgres.NewSwitchOps(o.Pool).List(ctx)
}

// SwitchHistory returns the most recent switch changes, newest first.
func (o Ops) SwitchHistory(ctx context.Context, limit int) ([]postgres.SwitchHistoryEntry, error) {
	return postgres.NewSwitchOps(o.Pool).History(ctx, limit)
}
