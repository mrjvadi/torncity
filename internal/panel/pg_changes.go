package panel

import (
	"context"
	"errors"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
)

// The changes, each through the same function the command line calls, so
// the audit rows are the command line's with the panel's operator as actor.

// LinkGroup links a Telegram group to a city.
func (p *PG) LinkGroup(ctx context.Context, g GroupChange, a operator.Actor) error {
	_, err := postgres.NewCityGroupRepository(p.Pool).Link(ctx, postgres.CityGroupChange{CityCode: g.City,
		ChatID: g.ChatID, BotKey: g.Bot, Language: g.Language, Actor: a.Name, Reason: a.Reason, At: a.At})
	return err
}

// UnlinkGroup removes a group's link.
func (p *PG) UnlinkGroup(ctx context.Context, g GroupChange, a operator.Actor) error {
	return postgres.NewCityGroupRepository(p.Pool).Unlink(ctx, postgres.CityGroupChange{CityCode: g.City,
		ChatID: g.ChatID, Actor: a.Name, Reason: a.Reason, At: a.At})
}

// GrantDefence grants a defence contractor licence.
func (p *PG) GrantDefence(ctx context.Context, company string, a operator.Actor) (int64, error) {
	no, _, err := postgres.GrantDefenceLicence(ctx, p.Pool, postgres.OperatorLicence{CompanyCode: company,
		Actor: a.Name, Reason: a.Reason, At: a.At})
	return no, err
}

// RevokeDefence revokes a company's licence in force.
func (p *PG) RevokeDefence(ctx context.Context, company string, a operator.Actor) (int64, error) {
	return postgres.RevokeDefenceLicence(ctx, p.Pool, postgres.OperatorLicence{CompanyCode: company,
		Actor: a.Name, Reason: a.Reason, At: a.At})
}

// ChangeSeat appoints a player to a seat or vacates it.
func (p *PG) ChangeSeat(ctx context.Context, s SeatChange, appoint bool, a operator.Actor) (Seat, error) {
	req := postgres.SeatChangeRequest{
		SeatRef:    postgres.SeatRef{OfficeCode: s.Office, JurisdictionKind: s.Kind, JurisdictionCode: s.Code, Seat: s.Seat},
		PlayerCode: s.Player, Actor: a.Name, Reason: a.Reason, At: a.At,
	}
	admin := postgres.NewGovernanceAdmin(p.Pool)
	var ch postgres.SeatChange
	var err error
	if appoint {
		ch, err = admin.Appoint(ctx, req)
	} else {
		ch, err = admin.Vacate(ctx, req)
	}
	if err != nil {
		return Seat{}, err
	}
	out := Seat{JurisdictionKind: ch.Jurisdiction.Kind, JurisdictionCode: ch.Jurisdiction.Code, Office: s.Office,
		Seat: s.Seat, TermEndsAt: ch.After.TermEndsAt}
	if appoint {
		since := ch.After.Since
		out.Holder, out.AcquiredBy, out.Since = ch.PlayerLabel, ch.After.AcquiredBy, &since
	}
	return out, nil
}

// OpenElection opens an election.
func (p *PG) OpenElection(ctx context.Context, office, kind, code string, a operator.Actor) (OpenedElection, error) {
	e, err := p.Ops.OpenElection(ctx, office, kind, code, a)
	if err != nil {
		return OpenedElection{}, err
	}
	return OpenedElection{No: e.No, Office: e.OfficeCode, CandidacyEndsAt: e.CandidacyEndsAt, VotingEndsAt: e.VotingEndsAt}, nil
}

// Grant pays an operator grant.
func (p *PG) Grant(ctx context.Context, player string, amount int64, a operator.Actor) (operator.Grant, error) {
	return p.Ops.GrantCash(ctx, player, amount, a)
}

// LoadContent validates the server's content files and makes them active.
func (p *PG) LoadContent(ctx context.Context, a operator.Actor) (ContentLoaded, error) {
	_, applied, err := p.Ops.LoadContent(ctx, p.ContentDir, a)
	if err != nil {
		return ContentLoaded{}, err
	}
	return ContentLoaded{Version: applied.Version, VersionID: applied.VersionID, Checksum: applied.Checksum,
		PlayersPlaced: applied.PlayersPlaced, ResidencesSet: applied.ResidencesSet, Jurisdictions: applied.Jurisdictions,
		OfficesCreated: int(applied.OfficesCreated)}, nil
}

// ClearFlag clears a watch flag.
func (p *PG) ClearFlag(ctx context.Context, no int64, a operator.Actor) error {
	return p.Ops.ClearFlag(ctx, no, a)
}

// SettleHold releases or returns a held payment.
func (p *PG) SettleHold(ctx context.Context, no int64, release bool, a operator.Actor) (Settled, error) {
	h, err := p.Ops.SettleHold(ctx, no, release, a)
	if err != nil {
		return Settled{}, err
	}
	return Settled{No: h.No, Status: h.Status, Amount: h.Amount, Method: h.Method, Transaction: h.SettleTransactionID}, nil
}

func (m Message) announcement(a operator.Actor) (postgres.OperatorAnnouncement, error) {
	if m.Text == "" {
		return postgres.OperatorAnnouncement{}, errors.New("the text is empty")
	}
	out := postgres.OperatorAnnouncement{Text: m.Text, Actor: a.Name, Reason: a.Reason, At: a.At, Only: m.Only}
	if m.TextEN != "" {
		out.Texts = map[string]string{"en": m.TextEN}
	}
	return out, nil
}

// Announce posts a text in every city's linked groups.
func (p *PG) Announce(ctx context.Context, m Message, a operator.Actor) (Announced, error) {
	m.Only = ""
	ann, err := m.announcement(a)
	if err != nil {
		return Announced{}, err
	}
	id, cities, err := postgres.Announce(ctx, p.Pool, ann)
	return Announced{EventID: id, Cities: cities}, err
}

// Broadcast sends a text to every active player's private chat, or to one
// player's (a preview).
func (p *PG) Broadcast(ctx context.Context, m Message, a operator.Actor) (int, error) {
	ann, err := m.announcement(a)
	if err != nil {
		return 0, err
	}
	return postgres.Broadcast(ctx, p.Pool, ann)
}
