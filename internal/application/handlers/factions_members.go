package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/faction"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file holds a faction's people: its members and their ranks,
// invitations and applications, kicking, promoting, passing the leadership,
// and leaving — the last member leaving disbands it.

// Members handles faction.members: the members, and what the viewer's rank
// lets them do to each.
func (h *FactionsHandler) Members(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.FactionMembersView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		mb, err := h.membership(ctx, tx, p.ID, false)
		if err != nil {
			return err
		}
		view, err = h.membersView(ctx, tx, def, mb, p.ID)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.FactionMembers(h.screen(meta, lang), view), nil
}

func (h *FactionsHandler) membersView(ctx context.Context, tx application.Tx, def content.FactionDef, mb *member,
	viewer string,
) (screens.FactionMembersView, error) {
	charter := def.Charter()
	v := screens.FactionMembersView{Ref: factionRef(*mb.faction), Max: h.rules.MaxMembers,
		CanInvite: charter.Can(mb.rank, faction.Invite)}
	members, err := tx.Factions().Members(ctx, mb.faction.ID)
	if err != nil {
		return v, err
	}
	for _, m := range members {
		who, err := playerNamed(ctx, tx, m.PlayerID)
		if err != nil {
			return v, err
		}
		line := screens.FactionMemberLine{Player: who, Rank: m.Rank, Self: m.PlayerID == viewer}
		r := faction.Rank(m.Rank)
		self := m.PlayerID == viewer
		line.CanKick = charter.CheckKick(mb.rank, r, self) == nil
		line.CanPromote = r == faction.Member && charter.CheckRank(mb.rank, r, faction.Officer, self) == nil
		line.CanDemote = r == faction.Officer && charter.CheckRank(mb.rank, r, faction.Member, self) == nil
		line.CanLead = mb.rank == faction.Leader && !self
		v.Members = append(v.Members, line)
	}
	pending, err := tx.Factions().Pending(ctx, mb.faction.ID)
	if err != nil {
		return v, err
	}
	canDecide := charter.Can(mb.rank, faction.Decide)
	for _, q := range pending {
		who, err := playerNamed(ctx, tx, q.PlayerID)
		if err != nil {
			return v, err
		}
		v.Requests = append(v.Requests, screens.FactionRequestLine{No: q.No, Kind: q.Kind, Player: who,
			CanDecide: canDecide && q.Kind == application.RequestApply})
	}
	return v, nil
}

// Invite handles faction.invite: a member whose rank allows invites a
// player by their code or username.
func (h *FactionsHandler) Invite(ctx context.Context, meta envelope.Metadata, req FactionCmd) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.To) == "" {
		return h.Members(ctx, meta)
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		invited  *screens.GovPlayer
		replayed bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}
		mb, err := h.membership(ctx, tx, p.ID, true)
		if err != nil {
			return err
		}
		if err := mb.may(def, faction.Invite); err != nil {
			return err
		}
		q, ok := ClassifyPlayerQuery(req.To)
		if !ok {
			return refuseFaction(screens.FactionRefusedNoPlayer)
		}
		target, err := h.search.Find(ctx, q)
		if isSentinel(err, application.ErrPlayerNotFound) {
			return refuseFaction(screens.FactionRefusedNoPlayer)
		}
		if err != nil {
			return err
		}
		if target.ID == p.ID {
			return refuseFaction(screens.FactionRefusedAlreadyMember)
		}
		if err := h.roomFor(ctx, tx, *mb.faction, target.ID); err != nil {
			return err
		}
		r, err := tx.Factions().Request(ctx, application.FactionRequest{ID: h.ids.NewID(), FactionID: mb.faction.ID,
			PlayerID: target.ID, Kind: application.RequestInvite, ByPlayer: p.ID, CreatedAt: h.now()})
		if isSentinel(err, application.ErrRequestPending) {
			return refuseFaction(screens.FactionRefusedPending)
		}
		if err != nil {
			return err
		}
		who := govPlayerOf(target)
		invited = &who
		return appendFactionEvent(ctx, tx, meta, "invited", mb.faction.ID, groupFields(*mb.faction, map[string]any{
			"player_id": target.ID, "no": r.No, "by_name": shownName(p), "by_code": p.PublicCode}))
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replayed || invited == nil {
		return h.Members(ctx, meta)
	}
	return screens.FactionInvited(h.screen(meta, lang), *invited), nil
}

// roomFor refuses a player who is in a faction, a faction that is full, or
// one with too many requests waiting.
func (h *FactionsHandler) roomFor(ctx context.Context, tx application.Tx, f application.Faction, playerID string) error {
	if _, err := tx.Factions().Membership(ctx, playerID); err == nil {
		return refuseFaction(screens.FactionRefusedTheirs)
	} else if !isSentinel(err, application.ErrNotInFaction) {
		return err
	}
	members, err := tx.Factions().Members(ctx, f.ID)
	if err != nil {
		return err
	}
	if len(members) >= h.rules.MaxMembers {
		r := refuseFaction(screens.FactionRefusedFull)
		r.view.Max = h.rules.MaxMembers
		return r
	}
	pending, err := tx.Factions().Pending(ctx, f.ID)
	if err != nil {
		return err
	}
	if len(pending) >= h.rules.MaxPending {
		return refuseFaction(screens.FactionRefusedPendingFull)
	}
	return nil
}

// Apply handles faction.apply: asking to join a faction. Without the
// confirmation it shows the faction; with it, its leader and the officers
// whose rank decides are asked.
func (h *FactionsHandler) Apply(ctx context.Context, meta envelope.Metadata, req FactionCmd) (*presenter.Response, error) {
	if !req.confirmed() {
		return h.View(ctx, meta, req)
	}
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		ref      screens.FactionRef
		replayed bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}
		f, err := h.byCode(ctx, tx, req.Code)
		if err != nil {
			return err
		}
		if f, err = tx.Factions().Lock(ctx, f.ID); err != nil {
			return err
		}
		if _, err := tx.Factions().Membership(ctx, p.ID); err == nil {
			return refuseFaction(screens.FactionRefusedAlreadyMember)
		} else if !isSentinel(err, application.ErrNotInFaction) {
			return err
		}
		if err := h.roomFor(ctx, tx, *f, p.ID); err != nil {
			return err
		}
		r, err := tx.Factions().Request(ctx, application.FactionRequest{ID: h.ids.NewID(), FactionID: f.ID,
			PlayerID: p.ID, Kind: application.RequestApply, ByPlayer: p.ID, CreatedAt: h.now()})
		if isSentinel(err, application.ErrRequestPending) {
			return refuseFaction(screens.FactionRefusedPending)
		}
		if err != nil {
			return err
		}
		ref = factionRef(*f)
		members, err := tx.Factions().Members(ctx, f.ID)
		if err != nil {
			return err
		}
		charter := def.Charter()
		for _, m := range members {
			if !charter.Can(faction.Rank(m.Rank), faction.Decide) {
				continue
			}
			if err := appendFactionEvent(ctx, tx, meta, "applied", f.ID, groupFields(*f, map[string]any{
				"player_id": m.PlayerID, "no": r.No, "by_name": shownName(p), "by_code": p.PublicCode})); err != nil {
				return err
			}
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replayed {
		return h.List(ctx, meta, FactionCmd{})
	}
	return screens.FactionApplied(h.screen(meta, lang), ref), nil
}

// Answer handles faction.answer: an invitee answering an invitation, or a
// member whose rank decides answering an application.
func (h *FactionsHandler) Answer(ctx context.Context, meta envelope.Metadata, req FactionCmd) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, err := strconv.ParseInt(strings.TrimSpace(req.No), 10, 64)
	accept := strings.TrimSpace(req.Verdict) == screens.FactionAccept
	if err != nil || no <= 0 || (!accept && strings.TrimSpace(req.Verdict) != screens.FactionDecline) {
		return h.Mine(ctx, meta)
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		answered screens.FactionAnsweredView
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}
		q, err := tx.Factions().RequestByNo(ctx, no)
		if isSentinel(err, application.ErrRequestNotFound) {
			return refuseFaction(screens.FactionRefusedRequestGone)
		}
		if err != nil {
			return err
		}
		f, err := tx.Factions().Lock(ctx, q.FactionID)
		if err != nil {
			return err
		}
		if q, err = tx.Factions().RequestByNo(ctx, no); err != nil {
			return err
		}
		if q.Status != application.RequestPending || !f.Active() {
			return refuseFaction(screens.FactionRefusedRequestGone)
		}
		switch q.Kind {
		case application.RequestInvite:
			if q.PlayerID != p.ID {
				return refuseFaction(screens.FactionRefusedNotYours)
			}
		default:
			m, err := tx.Factions().Membership(ctx, p.ID)
			if isSentinel(err, application.ErrNotInFaction) || (err == nil && m.FactionID != f.ID) {
				return refuseFaction(screens.FactionRefusedNotYours)
			}
			if err != nil {
				return err
			}
			if !def.Charter().Can(faction.Rank(m.Rank), faction.Decide) {
				return refuseFaction(screens.FactionRefusedRank)
			}
		}
		now := h.now()
		joiner, err := tx.Players().GetByID(ctx, q.PlayerID)
		if err != nil {
			return err
		}
		answered = screens.FactionAnsweredView{Ref: factionRef(*f), Kind: q.Kind, Accepted: accept, Player: govPlayerOf(joiner)}
		if accept {
			if err := h.roomFor(ctx, tx, *f, joiner.ID); err != nil {
				var r *factionRefusal
				if stderrors.As(err, &r) && r.view.Kind == screens.FactionRefusedPendingFull {
					// The request being answered is one of those waiting.
					err = nil
				}
				if err != nil {
					return err
				}
			}
			if err := tx.Factions().AddMember(ctx, application.FactionMember{PlayerID: joiner.ID, FactionID: f.ID,
				Rank: string(faction.Member), JoinedAt: now}); err != nil {
				if isSentinel(err, application.ErrAlreadyInFaction) {
					return refuseFaction(screens.FactionRefusedTheirs)
				}
				return err
			}
			if err := tx.Factions().DecideRequest(ctx, q.ID, application.RequestAccepted, p.ID, now); err != nil {
				return err
			}
			// Joining one faction withdraws what else they had waiting.
			if err := tx.Factions().WithdrawPendingOf(ctx, joiner.ID, now); err != nil {
				return err
			}
			if err := appendFactionEvent(ctx, tx, meta, "joined", f.ID, groupFields(*f, map[string]any{
				"player_id": joiner.ID, "player_name": shownName(joiner)})); err != nil {
				return err
			}
		} else if err := tx.Factions().DecideRequest(ctx, q.ID, application.RequestDeclined, p.ID, now); err != nil {
			return err
		}
		// The other side hears the answer.
		tell := q.ByPlayer
		if q.Kind == application.RequestApply {
			tell = q.PlayerID
		}
		return appendFactionEvent(ctx, tx, meta, "answered", f.ID, groupFields(*f, map[string]any{
			"player_id": tell, "kind": q.Kind, "accepted": accept, "by_name": shownName(p),
			"joiner_name": shownName(joiner), "joiner_code": joiner.PublicCode}))
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replayed {
		return h.Mine(ctx, meta)
	}
	return screens.FactionAnswered(h.screen(meta, lang), answered), nil
}

// target reads another member of the actor's faction by their public code.
func (h *FactionsHandler) target(ctx context.Context, tx application.Tx, mb *member, code string) (*application.Player, faction.Rank, error) {
	code = playercode.Normalize(code)
	if !playercode.Valid(code) {
		return nil, "", refuseFaction(screens.FactionRefusedNoPlayer)
	}
	p, err := h.search.Find(ctx, application.PlayerQuery{Kind: application.PlayerQueryPublicCode, PublicCode: code})
	if isSentinel(err, application.ErrPlayerNotFound) {
		return nil, "", refuseFaction(screens.FactionRefusedNoPlayer)
	}
	if err != nil {
		return nil, "", err
	}
	m, err := tx.Factions().Membership(ctx, p.ID)
	if isSentinel(err, application.ErrNotInFaction) || (err == nil && m.FactionID != mb.faction.ID) {
		return nil, "", refuseFaction(screens.FactionRefusedNotInIt)
	}
	if err != nil {
		return nil, "", err
	}
	return p, faction.Rank(m.Rank), nil
}

// Kick handles faction.kick: removing a member of lower rank, confirmed
// first.
func (h *FactionsHandler) Kick(ctx context.Context, meta envelope.Metadata, req FactionCmd) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		confirm  *screens.FactionConfirmView
		replayed bool
		done     bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		if req.confirmed() {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		mb, err := h.membership(ctx, tx, p.ID, req.confirmed())
		if err != nil {
			return err
		}
		target, rank, err := h.target(ctx, tx, mb, req.Player)
		if err != nil {
			return err
		}
		if err := def.Charter().CheckKick(mb.rank, rank, target.ID == p.ID); err != nil {
			return refuseFaction(screens.FactionRefusedRank)
		}
		if !req.confirmed() {
			confirm = &screens.FactionConfirmView{Kind: screens.FactionConfirmKick, Ref: factionRef(*mb.faction),
				Player: govPlayerOf(target)}
			return nil
		}
		if err := h.refuseInCrew(ctx, tx, target.ID); err != nil {
			return err
		}
		if err := tx.Factions().RemoveMember(ctx, mb.faction.ID, target.ID); err != nil {
			return err
		}
		done = true
		if err := appendFactionEvent(ctx, tx, meta, "kicked", mb.faction.ID, groupFields(*mb.faction, map[string]any{
			"player_id": target.ID, "by_name": shownName(p)})); err != nil {
			return err
		}
		return appendFactionEvent(ctx, tx, meta, "left", mb.faction.ID, groupFields(*mb.faction, map[string]any{
			"player_id": target.ID, "player_name": shownName(target), "kicked": true}))
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if confirm != nil {
		return screens.FactionConfirm(h.screen(meta, lang), *confirm), nil
	}
	_, _ = replayed, done
	return h.Members(ctx, meta)
}

// refuseInCrew refuses a member leaving while their crew is out on a job.
func (h *FactionsHandler) refuseInCrew(ctx context.Context, tx application.Tx, playerID string) error {
	op, err := tx.Factions().RunningCrew(ctx, playerID)
	switch {
	case isSentinel(err, application.ErrNoOperation):
		return nil
	case err != nil:
		return err
	case op.Status == application.HeistRunning:
		return refuseFaction(screens.FactionRefusedOnAJob)
	}
	return nil
}

// Rank handles faction.rank: promoting a member to officer, lowering an
// officer to member, or — the leader, confirmed — passing the leadership.
func (h *FactionsHandler) Rank(ctx context.Context, meta envelope.Metadata, req FactionCmd) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	to := faction.Rank(strings.TrimSpace(req.Rank))
	if !to.Valid() {
		return h.Members(ctx, meta)
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		confirm  *screens.FactionConfirmView
		replayed bool
	)
	lead := to == faction.Leader
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		writes := !lead || req.confirmed()
		if writes {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		mb, err := h.membership(ctx, tx, p.ID, writes)
		if err != nil {
			return err
		}
		target, from, err := h.target(ctx, tx, mb, req.Player)
		if err != nil {
			return err
		}
		if lead {
			if mb.rank != faction.Leader || target.ID == p.ID {
				return refuseFaction(screens.FactionRefusedRank)
			}
			if !req.confirmed() {
				confirm = &screens.FactionConfirmView{Kind: screens.FactionConfirmLead, Ref: factionRef(*mb.faction),
					Player: govPlayerOf(target)}
				return nil
			}
			// The old leader steps down first: one leader at a time.
			if err := tx.Factions().SetRank(ctx, mb.faction.ID, p.ID, string(faction.Officer)); err != nil {
				return err
			}
			if err := tx.Factions().SetRank(ctx, mb.faction.ID, target.ID, string(faction.Leader)); err != nil {
				return err
			}
			f := *mb.faction
			f.LeaderID, f.UpdatedAt = target.ID, h.now()
			if err := tx.Factions().Save(ctx, f); err != nil {
				return err
			}
		} else {
			if err := def.Charter().CheckRank(mb.rank, from, to, target.ID == p.ID); err != nil {
				if stderrors.Is(err, faction.ErrNoChange) {
					return nil
				}
				return refuseFaction(screens.FactionRefusedRank)
			}
			if err := tx.Factions().SetRank(ctx, mb.faction.ID, target.ID, string(to)); err != nil {
				return err
			}
		}
		return appendFactionEvent(ctx, tx, meta, "ranked", mb.faction.ID, groupFields(*mb.faction, map[string]any{
			"player_id": target.ID, "player_name": shownName(target), "rank": string(to), "by_name": shownName(p)}))
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if confirm != nil {
		return screens.FactionConfirm(h.screen(meta, lang), *confirm), nil
	}
	_ = replayed
	return h.Members(ctx, meta)
}

// Leave handles faction.leave: a member leaving, confirmed first. The leader
// passes the leadership before leaving; a leader alone disbands the faction,
// and what is left in its bank goes to their own.
func (h *FactionsHandler) Leave(ctx context.Context, meta envelope.Metadata, req FactionCmd) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		confirm  *screens.FactionConfirmView
		left     *screens.FactionLeftView
		replayed bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		if _, err := factionDef(snap); err != nil {
			return err
		}
		if req.confirmed() {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		mb, err := h.membership(ctx, tx, p.ID, req.confirmed())
		if err != nil {
			return err
		}
		members, err := tx.Factions().Members(ctx, mb.faction.ID)
		if err != nil {
			return err
		}
		disband := len(members) == 1
		if mb.rank == faction.Leader && !disband {
			return refuseFaction(screens.FactionRefusedLeaderLeaving)
		}
		if !req.confirmed() {
			kind := screens.FactionConfirmLeave
			if disband {
				kind = screens.FactionConfirmDisband
			}
			confirm = &screens.FactionConfirmView{Kind: kind, Ref: factionRef(*mb.faction)}
			return nil
		}
		if err := h.refuseInCrew(ctx, tx, p.ID); err != nil {
			return err
		}
		now := h.now()
		f := *mb.faction
		left = &screens.FactionLeftView{Ref: factionRef(f), Disbanded: disband}
		if disband {
			if op, err := tx.Factions().OpenOperation(ctx, f.ID); err == nil {
				op.Status = application.HeistCalledOff
				if err := tx.Factions().SaveOperation(ctx, *op); err != nil {
					return err
				}
			} else if !isSentinel(err, application.ErrNoOperation) {
				return err
			}
			treasury, err := tx.Ledger().AccountFor(ctx, application.AccountFactionTreasury, f.ID)
			if err != nil {
				return err
			}
			if b := treasury.Balance.Minor(); b > 0 {
				to, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, p.ID)
				if err != nil {
					return err
				}
				if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
					Reason: application.ReasonFactionWithdrawal, ReferenceType: application.FactionReference,
					ReferenceID: f.ID, CreatedAt: now,
					Entries: []application.LedgerEntry{{AccountID: treasury.ID, Amount: money.FromMinor(-b)},
						{AccountID: to.ID, Amount: money.FromMinor(b)}},
				}); err != nil {
					return err
				}
				left.PaidOut = b
			}
			if err := h.withdrawFactionRequests(ctx, tx, f.ID); err != nil {
				return err
			}
			f.Status, f.DisbandedAt, f.UpdatedAt = application.FactionDisbanded, &now, now
			if err := tx.Factions().Save(ctx, f); err != nil {
				return err
			}
		}
		if err := tx.Factions().RemoveMember(ctx, f.ID, p.ID); err != nil {
			return err
		}
		name := "left"
		if disband {
			name = "disbanded"
		}
		return appendFactionEvent(ctx, tx, meta, name, f.ID, groupFields(f, map[string]any{
			"player_id": p.ID, "player_name": shownName(p), "city_id": f.CityID}))
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	switch {
	case confirm != nil:
		return screens.FactionConfirm(h.screen(meta, lang), *confirm), nil
	case left != nil:
		return screens.FactionLeft(h.screen(meta, lang), *left), nil
	}
	_ = replayed
	return h.List(ctx, meta, FactionCmd{})
}

// withdrawFactionRequests withdraws every request waiting on a faction.
func (h *FactionsHandler) withdrawFactionRequests(ctx context.Context, tx application.Tx, factionID string) error {
	pending, err := tx.Factions().Pending(ctx, factionID)
	if err != nil {
		return err
	}
	for _, q := range pending {
		if err := tx.Factions().DecideRequest(ctx, q.ID, application.RequestWithdrawn, "", h.now()); err != nil &&
			!isSentinel(err, application.ErrRequestNotFound) {
			return err
		}
	}
	return nil
}
