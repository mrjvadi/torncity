package handlers

import (
	"context"
	stderrors "errors"
	"math"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/diplomacy"
	"github.com/mrjvadi/torncity/internal/domain/polity"
	"github.com/mrjvadi/torncity/internal/domain/raid"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Treaties between settlements, unions and countries (docs/adr/0076 7). The rows are those of the treaties between countries (ADR 0022):
// a party is a jurisdiction, and a settlement, a league and a country all have one. This screen is the way a settlement or a union signs
// one; the countries keep theirs. What a treaty lets its parties do is read from the row by the places that ask (diplomacy.Partners).

// VillageTreatyRequest is the payload of settlement.treaty.
type VillageTreatyRequest struct {
	// Action is "" (the screen) or one of village.TreatyAction*.
	Action string `json:"action,omitempty"`
	// Union names the union that acts (and not the settlement); No the treaty's number; Kind the kind of treaty; Code the other settlement's
	// code, ToUnion the other union's id.
	Union   string `json:"union,omitempty"`
	No      string `json:"no,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Code    string `json:"code,omitempty"`
	ToUnion string `json:"to_union,omitempty"`
}

type treatyParty struct {
	kind         string
	id           string // the settlement's city id or the union's id
	jurisdiction string
	name         presentation.Named
	settlement   application.FoundedSettlement
	union        *application.Union
}

func (h *VillageHandler) treatyActor(ctx context.Context, tx application.Tx, s application.FoundedSettlement, has bool, unionID string) (treatyParty, error) {
	if unionID != "" {
		u, err := tx.Unions().ByID(ctx, unionID, false)
		if err != nil || u == nil || u.Status != "active" {
			return treatyParty{}, refuseVillage(village.UnionNoUnion, village.AddrTreaty)
		}
		return treatyParty{kind: "union", id: u.ID, jurisdiction: u.JurisdictionID, name: named(u.ID, u.Name), union: u}, nil
	}
	if !has {
		return treatyParty{}, refuseVillage(village.VillageNoSettlement)
	}
	return treatyParty{kind: "settlement", id: s.CityID, jurisdiction: s.JurisdictionID, name: named(s.Code, s.Name), settlement: s}, nil
}

func (h *VillageHandler) treatyPower(ctx context.Context, tx application.Tx, pt treatyParty, p *application.Player, perm charter.Permission) error {
	var err error
	if pt.union != nil {
		_, err = h.requireUnionPower(ctx, tx, pt.union, p.ID, perm)
	} else {
		_, err = h.requireSettlementPower(ctx, tx, pt.settlement, p.ID, perm)
	}
	return err
}

// Treaty handles settlement.treaty.
func (h *VillageHandler) Treaty(ctx context.Context, meta envelope.Metadata, req VillageTreatyRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.SettlementTreatyView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, has, err := h.settlementOpt(ctx, tx, meta)
		if err != nil {
			return err
		}
		notice := ""
		if act := strings.TrimSpace(req.Action); act != "" {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if fresh {
				if notice, err = h.treatyAct(ctx, tx, meta, snap, s, has, p, act, req); err != nil {
					return err
				}
			}
		}
		view, err = h.treatyView(ctx, tx, snap, s, has, p, strings.TrimSpace(req.Union))
		view.Notice = notice
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.Treaty(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) treatyTypes(snap *content.Snapshot) map[string]diplomacy.TreatyType {
	out := map[string]diplomacy.TreatyType{}
	for _, t := range snap.TreatyTypes() {
		out[t.Code] = t.Type()
	}
	return out
}

func (h *VillageHandler) treatyAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, has bool, p *application.Player, act string, req VillageTreatyRequest) (string, error) {
	if !h.polityOpen() {
		return "", refuseVillage(village.TreatyNotYet, village.AddrTreaty)
	}
	pt, err := h.treatyActor(ctx, tx, s, has, strings.TrimSpace(req.Union))
	if err != nil {
		return "", err
	}
	now := h.now()
	switch act {
	case village.TreatyActionPropose:
		return village.TreatyNoticeProposed, h.treatyPropose(ctx, tx, meta, snap, pt, p, req, now)
	case village.TreatyActionAccept, village.TreatyActionDecline:
		return h.treatyAnswer(ctx, tx, meta, snap, pt, p, req, act == village.TreatyActionAccept, now)
	case village.TreatyActionWithdraw, village.TreatyActionEnd:
		return h.treatyEnd(ctx, tx, meta, snap, pt, p, req, act, now)
	}
	return "", refuseVillage(village.TreatyBadArgs, village.AddrTreaty)
}

// otherParty finds the other side by a settlement code or a union id.
func (h *VillageHandler) otherParty(ctx context.Context, tx application.Tx, req VillageTreatyRequest) (treatyParty, error) {
	if u := strings.TrimSpace(req.ToUnion); u != "" {
		un, err := tx.Unions().ByID(ctx, u, false)
		if err != nil || un == nil || un.Status != "active" {
			return treatyParty{}, refuseVillage(village.TreatyNoTarget, village.AddrTreaty)
		}
		return treatyParty{kind: "union", id: un.ID, jurisdiction: un.JurisdictionID, name: named(un.ID, un.Name), union: un}, nil
	}
	o, err := h.settlementByCode(ctx, tx, req.Code)
	if err != nil {
		return treatyParty{}, err
	}
	if o == nil {
		return treatyParty{}, refuseVillage(village.TreatyNoTarget, village.AddrTreaty)
	}
	return treatyParty{kind: "settlement", id: o.CityID, jurisdiction: o.JurisdictionID, name: named(o.Code, o.Name), settlement: *o}, nil
}

func (h *VillageHandler) treatyPropose(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, pt treatyParty, p *application.Player, req VillageTreatyRequest, now time.Time) error {
	if err := h.treatyPower(ctx, tx, pt, p, charter.TreatyPropose); err != nil {
		return err
	}
	other, err := h.otherParty(ctx, tx, req)
	if err != nil {
		return err
	}
	if other.jurisdiction == pt.jurisdiction {
		return refuseVillage(village.TreatySelf, village.AddrTreaty)
	}
	def, ok := snap.TreatyType(strings.TrimSpace(req.Kind))
	if !ok {
		return refuseVillage(village.TreatyNoKind, village.AddrTreaty)
	}
	if err := lockParties(ctx, tx, pt.jurisdiction, other.jurisdiction); err != nil {
		return err
	}
	if err := tx.Diplomacy().ExpireTreaties(ctx, pt.jurisdiction, other.jurisdiction, now); err != nil {
		return err
	}
	existing, err := tx.Diplomacy().TreatiesBetween(ctx, pt.jurisdiction, other.jurisdiction)
	if err != nil {
		return err
	}
	rules := make([]diplomacy.Treaty, len(existing))
	for i, t := range existing {
		rules[i] = t.Rule()
	}
	if err := diplomacy.CheckPropose(pt.jurisdiction, other.jurisdiction, def.Code, rules, now); err != nil {
		return refuseVillage(village.TreatyOpen, village.AddrTreaty)
	}
	office := h.officeTitleOf(ctx, tx, pt, p)
	t, err := tx.Diplomacy().ProposeTreaty(ctx, application.Treaty{ID: h.ids.NewID(), Kind: def.Code, ProposerID: pt.jurisdiction, PartnerID: other.jurisdiction, ProposedBy: p.ID,
		ProposedOffice: office, ProposedAt: now, ExpiresAt: now.AddDate(0, 0, int(max(h.polity.RatifyDays, 1)))})
	if stderrors.Is(err, application.ErrTreatyOpen) {
		return refuseVillage(village.TreatyOpen, village.AddrTreaty)
	}
	if err != nil {
		return err
	}
	if err := tx.Diplomacy().RecordEvent(ctx, application.DiplomacyEvent{ID: h.ids.NewID(), Kind: application.EventTreatyProposed, CountryID: pt.jurisdiction, OtherCountryID: other.jurisdiction,
		TreatyID: t.ID, PlayerID: p.ID, OfficeCode: office, At: now}); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "treaty_proposed", pt.id, map[string]any{"treaty": t.ID, "no": t.No, "kind": def.Code, "from": pt.id, "to": other.id})
}

// officeTitleOf is the title of the office the player acts from, for the public record.
func (h *VillageHandler) officeTitleOf(ctx context.Context, tx application.Tx, pt treatyParty, p *application.Player) string {
	if pt.union != nil {
		if os, err := tx.Unions().OfficesOf(ctx, pt.union.ID, p.ID); err == nil && len(os) > 0 {
			return os[0].Title
		}
		return "union"
	}
	return "charter"
}

func (h *VillageHandler) treatyOf(ctx context.Context, tx application.Tx, pt treatyParty, noText string) (*application.Treaty, error) {
	no := atoi64(noText)
	t, err := tx.Diplomacy().TreatyByNo(ctx, no, true)
	if err != nil || t == nil {
		return nil, refuseVillage(village.TreatyNoTreaty, village.AddrTreaty)
	}
	if t.ProposerID != pt.jurisdiction && t.PartnerID != pt.jurisdiction {
		return nil, refuseVillage(village.TreatyNotParty, village.AddrTreaty)
	}
	return t, nil
}

func (h *VillageHandler) treatyAnswer(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, pt treatyParty, p *application.Player, req VillageTreatyRequest, accept bool, now time.Time) (string, error) {
	if err := h.treatyPower(ctx, tx, pt, p, charter.TreatySign); err != nil {
		return "", err
	}
	t, err := h.treatyOf(ctx, tx, pt, req.No)
	if err != nil {
		return "", err
	}
	if err := lockParties(ctx, tx, t.ProposerID, t.PartnerID); err != nil {
		return "", err
	}
	if t, err = tx.Diplomacy().TreatyByNo(ctx, t.No, true); err != nil {
		return "", err
	}
	status, err := diplomacy.Answer(t.Rule(), pt.jurisdiction, accept, now)
	if err != nil {
		return "", refuseVillage(village.TreatyState, village.AddrTreaty)
	}
	office := h.officeTitleOf(ctx, tx, pt, p)
	t.Status, t.DecidedBy, t.DecidedOffice = status, p.ID, office
	t.DecidedAt = &now
	if err := tx.Diplomacy().SaveTreaty(ctx, *t); err != nil {
		return "", err
	}
	kind, notice := application.EventTreatyDeclined, village.TreatyNoticeDeclined
	if status == diplomacy.Active {
		kind, notice = application.EventTreatySigned, village.TreatyNoticeAccepted
	}
	if err := tx.Diplomacy().RecordEvent(ctx, application.DiplomacyEvent{ID: h.ids.NewID(), Kind: kind, CountryID: pt.jurisdiction, OtherCountryID: t.Rule().Other(pt.jurisdiction),
		TreatyID: t.ID, PlayerID: p.ID, OfficeCode: office, At: now}); err != nil {
		return "", err
	}
	return notice, appendVillageEvent(ctx, tx, meta, "treaty_"+string(status), pt.id, map[string]any{"treaty": t.ID, "no": t.No, "kind": t.Kind})
}

// treatyEnd withdraws an unanswered proposal or ends a treaty in force. An alliance or a non-aggression pact ended by one party is recorded
// (EndedParty) and cools that party for treaty.cooling_days: it may not declare a war or a raid on the other (docs/adr/0076 7.2).
func (h *VillageHandler) treatyEnd(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, pt treatyParty, p *application.Player, req VillageTreatyRequest, act string, now time.Time) (string, error) {
	perm := charter.TreatyEnd
	if act == village.TreatyActionWithdraw {
		perm = charter.TreatyPropose
	}
	if err := h.treatyPower(ctx, tx, pt, p, perm); err != nil {
		return "", err
	}
	t, err := h.treatyOf(ctx, tx, pt, req.No)
	if err != nil {
		return "", err
	}
	if err := lockParties(ctx, tx, t.ProposerID, t.PartnerID); err != nil {
		return "", err
	}
	if t, err = tx.Diplomacy().TreatyByNo(ctx, t.No, true); err != nil {
		return "", err
	}
	status, err := diplomacy.End(t.Rule(), pt.jurisdiction, now)
	if err != nil {
		return "", refuseVillage(village.TreatyState, village.AddrTreaty)
	}
	office := h.officeTitleOf(ctx, tx, pt, p)
	t.Status, t.EndedBy, t.EndedOffice, t.EndedAt, t.EndedParty = status, p.ID, office, &now, pt.jurisdiction
	if err := tx.Diplomacy().SaveTreaty(ctx, *t); err != nil {
		return "", err
	}
	kind, notice := application.EventTreatyWithdrawn, village.TreatyNoticeWithdrawn
	if status == diplomacy.Terminated {
		kind, notice = application.EventTreatyTerminated, village.TreatyNoticeEnded
	}
	if err := tx.Diplomacy().RecordEvent(ctx, application.DiplomacyEvent{ID: h.ids.NewID(), Kind: kind, CountryID: pt.jurisdiction, OtherCountryID: t.Rule().Other(pt.jurisdiction),
		TreatyID: t.ID, PlayerID: p.ID, OfficeCode: office, At: now}); err != nil {
		return "", err
	}
	return notice, appendVillageEvent(ctx, tx, meta, "treaty_"+string(status), pt.id, map[string]any{"treaty": t.ID, "no": t.No, "kind": t.Kind})
}

// ---- readers of the treaties -------------------------------------------------------------------------------------------------------

// polityJurisdictions are the jurisdictions a settlement acts through: its own and those of the unions it belongs to.
func (h *VillageHandler) polityJurisdictions(ctx context.Context, tx application.Tx, settlementID, settlementJID string) ([]string, error) {
	out := []string{settlementJID}
	ms, err := tx.Unions().MembershipsOf(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		u, err := tx.Unions().ByID(ctx, m.UnionID, false)
		if err != nil {
			return nil, err
		}
		if u != nil && u.Status == "active" {
			out = append(out, u.JurisdictionID)
		}
	}
	return out, nil
}

// sharedUnion is a union two settlements both belong to, nil for none; with a class, one whose delegations include it.
func (h *VillageHandler) sharedUnion(ctx context.Context, tx application.Tx, a, b string, class polity.Class) (*application.Union, error) {
	ma, err := tx.Unions().MembershipsOf(ctx, a)
	if err != nil {
		return nil, err
	}
	mb, err := tx.Unions().MembershipsOf(ctx, b)
	if err != nil {
		return nil, err
	}
	for _, x := range ma {
		for _, y := range mb {
			if x.UnionID != y.UnionID {
				continue
			}
			u, err := tx.Unions().ByID(ctx, x.UnionID, false)
			if err != nil || u == nil || u.Status != "active" {
				continue
			}
			if class == "" {
				return u, nil
			}
			for _, c := range u.Delegations {
				if c == class {
					return u, nil
				}
			}
		}
	}
	return nil, nil
}

// treatiesAmong lists the treaties between any jurisdiction of one side and any of the other.
func (h *VillageHandler) treatiesAmong(ctx context.Context, tx application.Tx, as, bs []string) ([]diplomacy.Treaty, error) {
	var out []diplomacy.Treaty
	seen := map[string]bool{}
	for _, a := range as {
		for _, b := range bs {
			if a == b {
				continue
			}
			ts, err := tx.Diplomacy().TreatiesBetween(ctx, a, b)
			if err != nil {
				return nil, err
			}
			for _, t := range ts {
				if !seen[t.ID] {
					seen[t.ID] = true
					out = append(out, t.Rule())
				}
			}
		}
	}
	return out, nil
}

// passageAllowed says whether settlement a may lay a way over the claims of settlement b: the same owner, a union of the two that
// delegates roads, or a passage treaty between them (or between unions they belong to).
func (h *VillageHandler) passageAllowed(ctx context.Context, tx application.Tx, snap *content.Snapshot, a, b application.FoundedSettlement, now time.Time) (bool, error) {
	if a.CityID == b.CityID {
		return true, nil
	}
	if u, err := h.sharedUnion(ctx, tx, a.CityID, b.CityID, polity.Roads); err != nil || u != nil {
		return u != nil, err
	}
	as, err := h.polityJurisdictions(ctx, tx, a.CityID, a.JurisdictionID)
	if err != nil {
		return false, err
	}
	bs, err := h.polityJurisdictions(ctx, tx, b.CityID, b.JurisdictionID)
	if err != nil {
		return false, err
	}
	ts, err := h.treatiesAmong(ctx, tx, as, bs)
	if err != nil {
		return false, err
	}
	types := h.treatyTypes(snap)
	for _, x := range as {
		for _, y := range bs {
			if diplomacy.MayPass(ts, types, x, y, now) {
				return true, nil
			}
		}
	}
	return false, nil
}

// passageCheck refuses a way over the claims of another settlement unless a passage is agreed with its owner (docs/adr/0042 4.10,
// docs/adr/0076 7.3). The ends of the way are the two settlements' own and need nothing. Before the rules start, nothing is checked.
func (h *VillageHandler) passageCheck(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, other application.FoundedSettlement, tiles []worldgen.TileID, back string) error {
	if !h.polityOpen() || len(tiles) == 0 {
		return nil
	}
	keys := make([]application.TileKey, 0, len(tiles))
	for _, t := range tiles {
		f, gx, gy := t.Parts()
		keys = append(keys, application.TileKey{Face: int(f), GX: gx, GY: gy})
	}
	owners, err := tx.Unions().ClaimOwners(ctx, keys)
	if err != nil {
		return err
	}
	now := h.now()
	for _, owner := range owners {
		if owner == s.CityID || owner == other.CityID {
			continue
		}
		o, err := tx.Settlements().ByID(ctx, owner)
		if err != nil {
			return err
		}
		ok, err := h.passageAllowed(ctx, tx, snap, s, o, now)
		if err != nil {
			return err
		}
		if !ok {
			return refuseVillage(village.HaulNoPassage, back)
		}
	}
	return nil
}

// aggressionBlocked says why settlement a may not raid or make war on settlement b right now: a non-aggression pact or an alliance between
// them, a union they both belong to, or the cooling-off after it ended one (docs/adr/0076 7.2). Empty means it may.
func (h *VillageHandler) aggressionBlocked(ctx context.Context, tx application.Tx, snap *content.Snapshot, a, b application.FoundedSettlement, now time.Time) (string, error) {
	if !h.polityOpen() {
		return "", nil
	}
	if u, err := h.sharedUnion(ctx, tx, a.CityID, b.CityID, ""); err != nil {
		return "", err
	} else if u != nil {
		return village.RaidSameUnion, nil
	}
	as, err := h.polityJurisdictions(ctx, tx, a.CityID, a.JurisdictionID)
	if err != nil {
		return "", err
	}
	bs, err := h.polityJurisdictions(ctx, tx, b.CityID, b.JurisdictionID)
	if err != nil {
		return "", err
	}
	ts, err := h.treatiesAmong(ctx, tx, as, bs)
	if err != nil {
		return "", err
	}
	types := h.treatyTypes(snap)
	for _, x := range as {
		for _, y := range bs {
			if diplomacy.Partners(ts, types, x, y, now, func(t diplomacy.TreatyType) bool { return t.NonAggression || t.MutualDefence }) {
				return village.RaidTreaty, nil
			}
			if _, cooling := diplomacy.CoolingUntil(ts, types, x, y, h.polity.TreatyCooling, now); cooling {
				return village.RaidCooling, nil
			}
		}
	}
	return "", nil
}

// ---- the screen -------------------------------------------------------------------------------------------------------------------

func (h *VillageHandler) treatyView(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, has bool, p *application.Player, unionID string) (village.SettlementTreatyView, error) {
	v := village.SettlementTreatyView{Open: h.polityOpen()}
	pt, err := h.treatyActor(ctx, tx, s, has, unionID)
	if err != nil {
		if has || unionID != "" {
			return v, err
		}
		return v, nil
	}
	if has {
		v.Village = s.Name
	}
	v.Party, v.PartyKind = pt.name, pt.kind
	v.MayPropose = h.treatyPower(ctx, tx, pt, p, charter.TreatyPropose) == nil
	v.MaySign = h.treatyPower(ctx, tx, pt, p, charter.TreatySign) == nil
	v.MayEnd = h.treatyPower(ctx, tx, pt, p, charter.TreatyEnd) == nil
	now := h.now()
	types := h.treatyTypes(snap)
	list, err := tx.Diplomacy().Treaties(ctx, pt.jurisdiction, now.AddDate(0, 0, -30))
	if err != nil {
		return v, err
	}
	rules := make([]diplomacy.Treaty, 0, len(list))
	for _, t := range list {
		rules = append(rules, t.Rule())
	}
	for _, t := range list {
		otherID := t.PartnerID
		if t.PartnerID == pt.jurisdiction {
			otherID = t.ProposerID
		}
		oj, err := tx.Governance().Jurisdiction(ctx, otherID)
		if err != nil {
			return v, err
		}
		ty := types[t.Kind]
		line := village.SettlementTreatyLine{No: t.No, Kind: named(t.Kind, t.Kind), Other: named(oj.Code, oj.Name), Status: string(t.Rule().StatusAt(now)), Mine: t.ProposerID == pt.jurisdiction,
			ProposedAt: t.ProposedAt, ExpiresAt: t.ExpiresAt, EndedAt: t.EndedAt, Passage: ty.Passage, Alliance: ty.MutualDefence}
		switch t.EndedParty {
		case "":
		case pt.jurisdiction:
			n := pt.name
			line.EndedParty = &n
		case otherID:
			n := named(oj.Code, oj.Name)
			line.EndedParty = &n
		}
		if until, cooling := diplomacy.CoolingUntil(rules, types, pt.jurisdiction, otherID, h.polity.TreatyCooling, now); cooling && t.Kind != "" {
			u := until
			line.CoolingUntil = &u
		}
		v.Treaties = append(v.Treaties, line)
	}
	for _, d := range snap.TreatyTypes() {
		v.Kinds = append(v.Kinds, village.SettlementTreatyKindLine{Kind: named(d.Code, d.Name), Alliance: d.MutualDefence, NonAggression: d.NonAggression, Passage: d.Passage, TariffDiscountBPS: d.TariffDiscountBPS})
	}
	founded, err := tx.Settlements().Founded(ctx)
	if err != nil {
		return v, err
	}
	for _, f := range founded {
		if f.JurisdictionID != pt.jurisdiction {
			v.Targets = append(v.Targets, named(f.Code, f.Name))
			v.Parties = append(v.Parties, village.TreatyPartyLine{Party: named(f.Code, f.Name), Kind: "settlement"})
		}
	}
	unions, err := tx.Unions().Active(ctx)
	if err != nil {
		return v, err
	}
	for _, u := range unions {
		if u.JurisdictionID != pt.jurisdiction {
			v.Parties = append(v.Parties, village.TreatyPartyLine{Party: named(u.ID, u.Name), Kind: u.Kind})
		}
	}
	v.CoolingDays = int64(h.polity.TreatyCooling / (24 * time.Hour))
	return v, nil
}

// lockParties takes both parties' diplomacy locks, the lower id first. A party is any jurisdiction: a settlement's, a union's, a country's.
func lockParties(ctx context.Context, tx application.Tx, a, b string) error {
	first, second := a, b
	if second < first {
		first, second = second, first
	}
	if err := tx.Unions().LockParty(ctx, first); err != nil {
		return err
	}
	return tx.Unions().LockParty(ctx, second)
}

// alliedBeacons are the open beacon towers of the settlements allied with the defender by a treaty whose type carries the beacon relay, as nodes
// of the relay chain: their places on the plane of the defender's centre, taken from the settlements' places on the world and the lots of the
// towers. A tower is open when its settlement's last service day held it. Before the rules start, or with no alliance, there are none.
func (h *VillageHandler) alliedBeacons(ctx context.Context, tx application.Tx, snap *content.Snapshot, defender application.FoundedSettlement) ([]raid.Beacon, error) {
	if !h.polityOpen() {
		return nil, nil
	}
	founded, err := tx.Settlements().Founded(ctx)
	if err != nil {
		return nil, err
	}
	now := h.now()
	types := h.treatyTypes(snap)
	ours, err := h.polityJurisdictions(ctx, tx, defender.CityID, defender.JurisdictionID)
	if err != nil {
		return nil, err
	}
	var out []raid.Beacon
	var w *worldgen.World
	for _, o := range founded {
		if o.CityID == defender.CityID {
			continue
		}
		theirs, err := h.polityJurisdictions(ctx, tx, o.CityID, o.JurisdictionID)
		if err != nil {
			return nil, err
		}
		ts, err := h.treatiesAmong(ctx, tx, ours, theirs)
		if err != nil {
			return nil, err
		}
		if len(ts) == 0 {
			continue
		}
		allied := false
		for _, a := range ours {
			for _, b := range theirs {
				allied = allied || diplomacy.Allied(ts, types, a, b, now)
			}
		}
		if !allied {
			continue
		}
		buildings, err := h.buildingsWithWorks(ctx, tx, snap, o.CityID)
		if err != nil {
			return nil, err
		}
		day, err := tx.ServiceDays().Last(ctx, o.CityID)
		if err != nil {
			return nil, err
		}
		if day == nil {
			continue
		}
		if w == nil {
			if w, err = h.world(ctx); err != nil {
				return nil, err
			}
		}
		if int(defender.WorldCellID) >= len(w.Cells) || int(o.WorldCellID) >= len(w.Cells) || defender.WorldCellID < 0 || o.WorldCellID < 0 {
			continue
		}
		dp, op := w.Cells[defender.WorldCellID].Point, w.Cells[o.WorldCellID].Point
		const earthM = 6_371_000.0
		dx := (op.LonDeg - dp.LonDeg) * math.Pi / 180 * math.Cos(dp.LatDeg*math.Pi/180) * earthM
		dy := (op.LatDeg - dp.LatDeg) * math.Pi / 180 * earthM
		for _, p := range detectorPosts(snap, buildings) {
			if p.beacon() && day.HeldPost(p.b.ID) {
				out = append(out, raid.Beacon{ID: "ally:" + p.b.ID, X: dx + float64(p.b.LotX)*30, Y: dy + float64(p.b.LotY)*30, Held: true})
			}
		}
	}
	return out, nil
}
