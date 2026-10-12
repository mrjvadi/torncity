package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/polity"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Unions, countries, treaties of settlements, public tenders and sovereign loans (docs/adr/0076). This file holds what they share: the
// rules, the scheduler's tick, the permission checks of a settlement and of a union, and the audit.

// WithPolity sets the rules of the item (config polity.*). Until they are set, and before their date, every act of the item is refused.
func (h *VillageHandler) WithPolity(r application.PolityRules) *VillageHandler {
	h.polity = r
	return h
}

func (h *VillageHandler) polityOpen() bool { return h.polity.Open(h.now()) }

// PolityTickPayload is the jsonb of a polity_tick action.
type PolityTickPayload struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// Aux is a second id or a number the tick needs (a settlement, a period number, a stage).
	Aux string `json:"aux,omitempty"`
}

// schedulePolity puts one deadline of the item on the game clock. The action's reference is the object, so a replay finds the same row.
func (h *VillageHandler) schedulePolity(ctx context.Context, tx application.Tx, kind, refType, refID, aux, actor string, now, at time.Time) error {
	payload, err := json.Marshal(PolityTickPayload{Kind: kind, ID: refID, Aux: aux})
	if err != nil {
		return err
	}
	if actor == "" {
		actor = refID
	}
	return tx.GameActions().Schedule(ctx, application.GameAction{ID: h.ids.NewID(), ActionType: application.PolityTickActionType, ActorType: "system", ActorID: actor,
		ReferenceType: refType, ReferenceID: refID, Payload: payload, StartedAt: now, FinishAt: at})
}

// PolityTick handles the scheduler's polity.tick: one deadline of the item, dispatched by its kind. Every kind settles its object with a
// conditional update, so a redelivered or late tick changes nothing.
func (h *VillageHandler) PolityTick(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presentation.Response, error) {
	var pl PolityTickPayload
	if err := json.Unmarshal(req.Payload, &pl); err != nil {
		return nil, err
	}
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := h.now()
		switch pl.Kind {
		case application.TickProposal:
			return h.tickProposal(ctx, tx, meta, pl.ID, now)
		case application.TickBallot:
			return h.tickBallot(ctx, tx, meta, pl.ID, now)
		case application.TickMotion:
			return h.tickMotion(ctx, tx, meta, pl.ID, now)
		case application.TickExit:
			return h.tickExit(ctx, tx, meta, pl.ID, pl.Aux, now)
		case application.TickDues:
			return h.tickDues(ctx, tx, meta, pl.ID, now)
		case application.TickTender:
			return h.tickTender(ctx, tx, meta, pl.ID, now)
		case application.TickTenderDue:
			return h.tickTenderDue(ctx, tx, meta, pl.ID, now)
		case application.TickRetention:
			return h.tickRetention(ctx, tx, meta, pl.ID, now)
		case application.TickLoanDue:
			return h.tickLoan(ctx, tx, meta, pl.ID, now)
		case application.TickLoanOffer:
			return h.tickLoanOffer(ctx, tx, meta, pl.ID, now)
		case application.TickProjectShift:
			return h.tickProjectShift(ctx, tx, meta, pl.ID, now)
		case application.TickProjectStage:
			return h.tickProjectStage(ctx, tx, meta, pl.ID, pl.Aux, now)
		case application.TickContractClose:
			return h.tickContractClose(ctx, tx, meta, pl.ID, now)
		case application.TickContractDue:
			return h.tickContractDue(ctx, tx, meta, pl.ID, now)
		case application.TickContractRelease:
			return h.tickContractRelease(ctx, tx, meta, pl.ID, now)
		case application.TickLicenceAuction:
			return h.tickLicenceAuction(ctx, tx, meta, pl.ID, now)
		case application.TickLicenceNotice:
			return h.tickLicenceNotice(ctx, tx, meta, pl.ID, now)
		case application.TickLicenceExpiry:
			return h.tickLicenceExpiry(ctx, tx, meta, pl.ID, now)
		}
		_, err := h.lawTick(ctx, tx, meta, pl.Kind, pl.ID, pl.Aux, now)
		return err
	})
	return nil, err
}

// polityScope is who an act is done for: a settlement through its charter, or a union through its own.
type polityScope struct {
	Settlement application.FoundedSettlement
	// Union is set when the act is the union's.
	Union *application.Union
}

func (sc polityScope) isUnion() bool { return sc.Union != nil }

// scopeOf resolves the scope of an act: the union named by id when the player holds powers in it, else their settlement.
func (h *VillageHandler) settlementOpt(ctx context.Context, tx application.Tx, meta envelope.Metadata) (application.FoundedSettlement, bool, error) {
	s, err := h.settlementOf(ctx, tx, meta)
	if err != nil {
		if stderrors.Is(err, application.ErrCityNotFound) {
			return application.FoundedSettlement{}, false, nil
		}
		return application.FoundedSettlement{}, false, err
	}
	return s, true, nil
}

// delegatedAway says a power of a settlement is the union's: the settlement belongs to a union whose delegations include the class of the
// permission (docs/adr/0076 4.3). Its own offices then do not hold it.
func (h *VillageHandler) delegatedAway(ctx context.Context, tx application.Tx, settlementID string, perm charter.Permission) (*application.Union, error) {
	class, ok := polity.ClassOf(perm)
	if !ok {
		return nil, nil
	}
	ms, err := tx.Unions().MembershipsOf(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		u, err := tx.Unions().ByID(ctx, m.UnionID, false)
		if err != nil {
			return nil, err
		}
		if u == nil || u.Status != "active" {
			continue
		}
		for _, c := range u.Delegations {
			if c == class {
				return u, nil
			}
		}
	}
	return nil, nil
}

// requireSettlementPower is requireVillage for the powers a settlement hands to a union: it holds the permission through its charter and has
// not delegated its class away.
func (h *VillageHandler) requireSettlementPower(ctx context.Context, tx application.Tx, s application.FoundedSettlement, playerID string, perm charter.Permission) (int64, error) {
	limit, err := requirePermission(ctx, tx, s, playerID, perm)
	if err != nil {
		return 0, err
	}
	if u, err := h.delegatedAway(ctx, tx, s.CityID, perm); err != nil {
		return 0, err
	} else if u != nil {
		return 0, refuseVillage(village.UnionDelegatedAway, village.AddrUnion)
	}
	return limit, nil
}

// requireUnionPower is the check of an act of a union: the player holds the permission in the union's charter, and the class of the
// permission is among the union's delegations (the ceiling holds at saving; this holds at using).
func (h *VillageHandler) requireUnionPower(ctx context.Context, tx application.Tx, u *application.Union, playerID string, perm charter.Permission) (int64, error) {
	held, err := tx.Unions().HeldIn(ctx, u.ID, playerID)
	if err != nil {
		return 0, err
	}
	limit, ok := held.Has(perm)
	if !ok {
		return 0, application.ErrNotOfficeHolder.WithDetail("permission", string(perm))
	}
	if err := polity.Ceiling(u.Delegations, []charter.Grant{{Permission: perm}}); err != nil {
		return 0, refuseVillage(village.UnionCeiling, village.AddrUnion)
	}
	return limit, nil
}

// auditUnion writes a line of the union's log.
func (h *VillageHandler) auditUnion(ctx context.Context, tx application.Tx, unionID, actor, action, office string, at time.Time, detail map[string]any) error {
	return tx.Unions().Audit(ctx, application.UnionAuditRow{ID: h.ids.NewID(), UnionID: unionID, ActorID: actor, Action: action, OfficeID: office, Detail: detail, At: at})
}

// unionPurse is the union's money: a state treasury owned by its jurisdiction.
func unionPurse(ctx context.Context, tx application.Tx, u *application.Union) (application.Account, error) {
	return tx.Ledger().AccountFor(ctx, application.AccountStateTreasury, u.JurisdictionID)
}

func accountBalance(ctx context.Context, tx application.Tx, a application.Account) (int64, error) {
	bal, err := tx.Ledger().Balance(ctx, a.ID)
	if err != nil {
		return 0, err
	}
	return bal.Minor(), nil
}

// splitCodes reads a comma-separated list.
func splitCodes(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func atoi64(s string) int64 {
	var n int64
	neg := false
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "-") {
		neg, t = true, t[1:]
	}
	for _, r := range t {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int64(r-'0')
		if n > 1<<55 {
			return 0
		}
	}
	if neg {
		return -n
	}
	return n
}
