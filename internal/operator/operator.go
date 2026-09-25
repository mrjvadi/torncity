// Package operator holds the operator's audited actions that are more than
// one repository call — a grant, a watch decision, an election opened, a
// content load — so the command line (cmd/admin) and the web panel
// (cmd/panel) carry them out in exactly one way, with the same audit rows.
//
// Every action takes the operator (Actor) and a reason, and refuses an
// empty one before it touches anything.
package operator

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Actor is who does something, why, and when.
type Actor struct {
	Name   string
	Reason string
	At     time.Time
}

// ErrNoActor is an action with nobody, or no reason, to record.
var ErrNoActor = errors.New("operator: an audited action needs who does it and why")

func (a Actor) check() (Actor, error) {
	a.Name, a.Reason = strings.TrimSpace(a.Name), strings.TrimSpace(a.Reason)
	if a.Name == "" || a.Reason == "" {
		return a, ErrNoActor
	}
	if a.At.IsZero() {
		a.At = time.Now()
	}
	return a, nil
}

// Ops carries out the actions against one database.
type Ops struct {
	Pool *postgres.Pool
	// Language is player.default_language, for the unit of work and what an
	// election's announcement is written in by default.
	Language string
}

// Grant is one operator grant made.
type Grant struct {
	ID, PlayerID, Label string
	Amount              int64
	GrantedBy           string
}

// GrantCash pays amount (minor units) into one player's cash from
// system_source: the audit row first, so the intent is on record even if the
// grant fails, then the grant, whose row names the operator too.
func (o Ops) GrantCash(ctx context.Context, playerCode string, amount int64, actor Actor) (Grant, error) {
	actor, err := actor.check()
	if err != nil {
		return Grant{}, err
	}
	if amount <= 0 {
		return Grant{}, errors.New("operator: a grant must be above zero")
	}
	admin := postgres.NewEconomyAdmin(o.Pool)
	playerID, label, err := admin.PlayerByCode(ctx, playerCode)
	if err != nil {
		return Grant{}, err
	}
	g := Grant{PlayerID: playerID, Label: label, Amount: amount, GrantedBy: "admin:" + actor.Name}
	if err := admin.AppendAudit(ctx, postgres.AuditEntry{Actor: actor.Name, Action: "economy.grant",
		TargetType: "reward_grants", NewValue: map[string]any{"player": playerID, "amount": amount},
		Reason: actor.Reason, At: actor.At}); err != nil {
		return Grant{}, err
	}
	err = postgres.NewUnitOfWork(o.Pool, o.Language).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		grant, err := application.GrantAdminCash(ctx, tx.Ledger(), playerID, money.FromMinor(amount), g.GrantedBy, actor.At)
		g.ID = grant.ID
		return err
	})
	return g, err
}

// ClearFlag clears a watch flag an operator has looked into.
func (o Ops) ClearFlag(ctx context.Context, no int64, actor Actor) error {
	actor, err := actor.check()
	if err != nil {
		return err
	}
	if err := postgres.NewEconomyAdmin(o.Pool).AppendAudit(ctx, postgres.AuditEntry{Actor: actor.Name,
		Action: "watch.clear", TargetType: "watch_flags", NewValue: map[string]any{"no": no},
		Reason: actor.Reason, At: actor.At}); err != nil {
		return err
	}
	return postgres.NewWatchRepository(o.Pool).Clear(ctx, no, "admin:"+actor.Name, actor.Reason, actor.At)
}

// SettleHold pays a held payment on to its payee (release) or gives it back
// to its payer.
func (o Ops) SettleHold(ctx context.Context, no int64, release bool, actor Actor) (application.PaymentHold, error) {
	actor, err := actor.check()
	if err != nil {
		return application.PaymentHold{}, err
	}
	verb := "return"
	if release {
		verb = "release"
	}
	if err := postgres.NewEconomyAdmin(o.Pool).AppendAudit(ctx, postgres.AuditEntry{Actor: actor.Name,
		Action: "watch." + verb, TargetType: "payment_holds", NewValue: map[string]any{"no": no},
		Reason: actor.Reason, At: actor.At}); err != nil {
		return application.PaymentHold{}, err
	}
	var held application.PaymentHold
	err = postgres.NewUnitOfWork(o.Pool, o.Language).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var err error
		held, err = application.SettleHold(ctx, tx, no, release, "admin:"+actor.Name, actor.Reason, actor.At)
		return err
	})
	return held, err
}

// newID draws a random (version 4) uuid.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("operator: no randomness available for identifiers: " + err.Error())
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type ids struct{}

func (ids) NewID() string { return newID() }

// OpenElection opens an election of an elected office in one place, with
// its announcement and audit row.
func (o Ops) OpenElection(ctx context.Context, office, kind, code string, actor Actor) (application.Election, error) {
	actor, err := actor.check()
	if err != nil {
		return application.Election{}, err
	}
	pack, err := postgres.NewContentStore(o.Pool).LoadActive(ctx)
	if err != nil {
		return application.Election{}, err
	}
	snap, err := content.BuildSnapshot(pack.Version, pack)
	if err != nil {
		return application.Election{}, err
	}
	j, err := postgres.NewGovernanceAdmin(o.Pool).JurisdictionByCode(ctx, kind, code)
	if err != nil {
		return application.Election{}, err
	}
	now := actor.At.UTC()
	meta := envelope.Metadata{RequestID: newID(), TraceID: newID(), Command: "election.open",
		Language: o.Language, SchemaVersion: envelope.SchemaVersion}
	var opened application.Election
	if err := postgres.NewUnitOfWork(o.Pool, o.Language).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		e, err := handlers.OpenElection(ctx, tx, ids{}, snap, office, j.ID, now)
		if err != nil {
			return err
		}
		opened = e
		return handlers.AnnounceElectionOpened(ctx, tx, postgres.NewCityRepository(o.Pool), meta, e)
	}); err != nil {
		return application.Election{}, err
	}
	err = postgres.NewEconomyAdmin(o.Pool).AppendAudit(ctx, postgres.AuditEntry{
		Actor: actor.Name, Action: "election.open", TargetType: "election",
		NewValue: map[string]any{"election_id": opened.ID, "no": opened.No, "office": opened.OfficeCode,
			"jurisdiction": kind + ":" + code, "voting_ends_at": opened.VotingEndsAt},
		Reason: actor.Reason, At: now,
	})
	return opened, err
}

// LoadAndValidate reads the content directory and checks it: the one step
// `content validate` and `content load` share, so validate answers exactly
// the question load asks.
func LoadAndValidate(dir string) (*content.Pack, error) {
	pack, err := content.Load(dir)
	if err != nil {
		return nil, err
	}
	if err := pack.Validate(); err != nil {
		return nil, err
	}
	return pack, nil
}

// LoadContent validates the content directory and stores it as the new
// active version, audited.
func (o Ops) LoadContent(ctx context.Context, dir string, actor Actor) (*content.Pack, postgres.Applied, error) {
	actor, err := actor.check()
	if err != nil {
		return nil, postgres.Applied{}, err
	}
	pack, err := LoadAndValidate(dir)
	if err != nil {
		return nil, postgres.Applied{}, err
	}
	applied, err := postgres.NewContentStore(o.Pool).Apply(ctx, pack, postgres.ApplyRequest{Actor: actor.Name, Reason: actor.Reason})
	return pack, applied, err
}
