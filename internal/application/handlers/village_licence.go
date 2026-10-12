package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/licence"
	"github.com/mrjvadi/torncity/internal/domain/tender"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The licence market (docs/adr/0077 7): rights a settlement grants, priced, time-limited, public and revocable only under rules.
//
//   - WHO WORKS THERE. The holders of licence.set_kind, licence.issue, licence.auction and licence.revoke in the settlement's charter; the clerk's
//     work is the engine's (the register, the audit, the ticks that close an auction or end a notice).
//   - WHAT IT CONSUMES. The fee, the winning bid, the renewal, the listing fee and the transfer fee; the bids of an open auction are held in its escrow.
//   - WHAT IT PROVIDES. The right itself, which the action it gates asks for: a stall sells only with a stall right, a craft is worked only with its
//     privilege. Money goes to the settlement's treasury (fees, prices, the issuer's share of a sale) or to the seller (the rest of a sale).
//   - WHAT BREAKS. A missing licence stops the action it gates, once the settlement requires it and its grace is over, and the refusal says so.

// VillageLicenceRequest is the payload of settlement.licence.
type VillageLicenceRequest struct {
	// Action is "" (the screen) or one of village.LicenceAction*.
	Action string `json:"action,omitempty"`
	// ID names a licence, an application, an auction or a listing; Code a kind of right; Target the thing an exclusive right is over.
	ID     string `json:"id,omitempty"`
	Code   string `json:"code,omitempty"`
	Target string `json:"target,omitempty"`
	// Company is a company's public code when it, not the player, is the holder or the bidder.
	Company string `json:"company,omitempty"`
	// Mode, Fee, Reserve, Term and Transferable are a requirement; Amount a bid; Window the hours an auction is open; Price a listing's price.
	Mode         string `json:"mode,omitempty"`
	Fee          string `json:"fee,omitempty"`
	Reserve      string `json:"reserve,omitempty"`
	Term         string `json:"term,omitempty"`
	Transferable string `json:"transferable,omitempty"`
	Amount       string `json:"amount,omitempty"`
	Window       string `json:"window,omitempty"`
	Price        string `json:"price,omitempty"`
	// Cause is why a revocation is made: non_use, or empty for one without cause.
	Cause string `json:"cause,omitempty"`
}

func (h *VillageHandler) licenceRules() licence.Rules {
	t := h.special.Licence
	return licence.Rules{Grace: t.Grace, AuctionMin: t.AuctionMin, ReserveMin: t.ReserveMin, TransferFeeBPS: t.TransferFeeBPS, TransferCooldown: t.TransferCooldown,
		MaxShareBPS: t.MaxShareBPS, ShareCapFrom: t.ShareCapFrom, Notice: t.Notice, Nonuse: t.Nonuse, TermMax: t.TermMax, ListingFee: t.ListingFee}
}

// Licence handles settlement.licence.
func (h *VillageHandler) Licence(ctx context.Context, meta envelope.Metadata, req VillageLicenceRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.LicenceView
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
		if act := strings.TrimSpace(req.Action); act != "" && act != village.LicenceActionOpen {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if fresh {
				if notice, err = h.licenceAct(ctx, tx, meta, snap, s, has, p, act, req); err != nil {
					return err
				}
			}
		}
		view, err = h.licenceView(ctx, tx, snap, s, has, p)
		view.Notice = notice
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.Licence(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) licenceAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, has bool,
	p *application.Player, act string, req VillageLicenceRequest,
) (string, error) {
	if !h.licenceOpen() {
		return "", refuseVillage(village.LicenceNotYet, village.AddrLicence)
	}
	now := h.now()
	switch act {
	case village.LicenceActionSetKind:
		return village.LicenceNoticeKindSet, h.licenceSetKind(ctx, tx, snap, s, has, p, req, now)
	case village.LicenceActionUnsetKind:
		return village.LicenceNoticeKindShut, h.licenceUnsetKind(ctx, tx, s, has, p, req, now)
	case village.LicenceActionClaim:
		return village.LicenceNoticeGranted, h.licenceClaim(ctx, tx, snap, s, has, p, req, now)
	case village.LicenceActionApply:
		return village.LicenceNoticeApplied, h.licenceApply(ctx, tx, snap, s, has, p, req, now)
	case village.LicenceActionIssue:
		return village.LicenceNoticeGranted, h.licenceIssue(ctx, tx, snap, s, has, p, req, now)
	case village.LicenceActionDecline:
		return village.LicenceNoticeDeclined, h.licenceDecide(ctx, tx, s, has, p, req, "refused", now)
	case village.LicenceActionWithdraw:
		return village.LicenceNoticeWithdrawn, h.licenceWithdraw(ctx, tx, p, req, now)
	case village.LicenceActionAuction:
		return village.LicenceNoticeAuction, h.licenceAuction(ctx, tx, snap, s, has, p, req, now)
	case village.LicenceActionBid:
		return village.LicenceNoticeBid, h.licenceBid(ctx, tx, p, req, now)
	case village.LicenceActionRenew:
		return village.LicenceNoticeRenewed, h.licenceRenew(ctx, tx, p, req, now)
	case village.LicenceActionList:
		return village.LicenceNoticeListed, h.licenceList(ctx, tx, p, req, now)
	case village.LicenceActionBuy:
		return village.LicenceNoticeBought, h.licenceBuy(ctx, tx, p, req, now)
	case village.LicenceActionDelist:
		return village.LicenceNoticeDelisted, h.licenceDelist(ctx, tx, p, req, now)
	case village.LicenceActionRevoke:
		return h.licenceRevoke(ctx, tx, s, has, p, req, now)
	}
	return "", refuseVillage(village.LicenceBadArgs, village.AddrLicence)
}

func (h *VillageHandler) licenceAudit(ctx context.Context, tx application.Tx, issuerID, licenceID, actor, kind string, detail map[string]any, now time.Time) error {
	if detail == nil {
		detail = map[string]any{}
	}
	return tx.Licences().Event(ctx, application.LicenceEvent{ID: h.ids.NewID(), IssuerID: issuerID, LicenceID: licenceID, ActorID: actor, Kind: kind, Detail: detail, At: now})
}

func requirementOf(r application.LicenceRule) licence.Requirement {
	return licence.Requirement{Kind: r.Kind, Mode: r.Mode, Fee: r.Fee, Reserve: r.Reserve, Term: time.Duration(r.TermDays) * 24 * time.Hour, Transferable: r.Transferable, EffectiveAt: r.EffectiveAt}
}

// holderOf resolves who holds or bids: the player, or the player's company when its code is given.
func (h *VillageHandler) holderOf(ctx context.Context, tx application.Tx, p *application.Player, companyCode string) (kind, id string, err error) {
	if strings.TrimSpace(companyCode) == "" {
		return "player", p.ID, nil
	}
	co, err := ownsCompany(ctx, tx, p.ID, companyCode)
	if err != nil {
		return "", "", err
	}
	if co == nil {
		return "", "", refuseVillage(village.LicenceNotCompany, village.AddrLicence)
	}
	return "company", co.ID, nil
}

// officialsOf are the people whose licences the issuer may not grant them: the holders of the powers that issue, and the one acting.
func (h *VillageHandler) licenceOfficials(ctx context.Context, tx application.Tx, s application.FoundedSettlement, actor string) ([]string, error) {
	out := []string{}
	if actor != "" {
		out = append(out, actor)
	}
	for _, perm := range []charter.Permission{charter.LicenceIssue, charter.LicenceAuction, charter.LicenceRevoke, charter.LicenceSetKind} {
		hs, err := permissionHolders(ctx, tx, s, perm)
		if err != nil {
			return nil, err
		}
		out = append(out, hs...)
	}
	return out, nil
}

// conflicts says an official of the issuer is, or stands behind, the holder: the owner or manager or a shareholder of the company, or the
// player himself.
func (h *VillageHandler) licenceConflict(ctx context.Context, tx application.Tx, officials []string, holderKind, holderID string) (bool, error) {
	if holderKind == "player" {
		for _, o := range officials {
			if o == holderID {
				return true, nil
			}
		}
		return false, nil
	}
	co, err := tx.Companies().ByID(ctx, holderID)
	if err != nil || co == nil {
		return false, err
	}
	stake, err := h.companyStake(ctx, tx, co)
	if err != nil {
		return false, err
	}
	return tender.Conflicts(officials, stake, h.special.Licence.CoiShareBPS), nil
}

func (h *VillageHandler) licenceSetKind(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, has bool, p *application.Player,
	req VillageLicenceRequest, now time.Time,
) error {
	if !has {
		return refuseVillage(village.VillageNoSettlement)
	}
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.LicenceSetKind); err != nil {
		return err
	}
	def, ok := snap.LicenceKind(strings.TrimSpace(req.Code))
	if !ok {
		return refuseVillage(village.LicenceNoKind, village.AddrLicence)
	}
	if missing, err := knowledgeMissing(ctx, tx, s.CityID, needsKnowledge(def.Requires)); err != nil {
		return err
	} else if len(missing) > 0 {
		return researchRefusal(village.LicenceResearch, village.AddrLicence, missing, snap)
	}
	rules := h.licenceRules()
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	fee, reserve, days := atoi64(req.Fee), atoi64(req.Reserve), atoi64(req.Term)
	if mode != licence.Fixed && mode != licence.Auction {
		return refuseVillage(village.LicenceBadArgs, village.AddrLicence)
	}
	if _, ok := rules.ValidTerm(time.Duration(days) * 24 * time.Hour); !ok {
		return refuseVillage(village.LicenceTermBad, village.AddrLicence)
	}
	if mode == licence.Auction && (!rules.ReserveOK(reserve)) {
		return refuseVillage(village.LicenceReserveLow, village.AddrLicence)
	}
	if mode == licence.Fixed && fee < 0 {
		return refuseVillage(village.LicenceBadArgs, village.AddrLicence)
	}
	if def.Exclusive() && mode == licence.Fixed {
		// an exclusive right is let by auction or on application: both are fine; the fixed mode is the application
	}
	rule := application.LicenceRule{SettlementID: s.CityID, Kind: def.Code, Mode: mode, Fee: fee, Reserve: reserve, TermDays: int(min(days, int64(rules.TermMax/(24*time.Hour)))),
		Transferable: req.Transferable == "1" || (req.Transferable == "" && def.Transferable), SetBy: p.ID, SetAt: now, EffectiveAt: rules.EffectiveFrom(now)}
	if err := tx.Licences().UpsertRule(ctx, rule); err != nil {
		return err
	}
	return h.licenceAudit(ctx, tx, s.CityID, "", p.ID, "kind_set", map[string]any{"kind": def.Code, "mode": mode, "fee": fee, "reserve": reserve, "term_days": rule.TermDays}, now)
}

func (h *VillageHandler) licenceUnsetKind(ctx context.Context, tx application.Tx, s application.FoundedSettlement, has bool, p *application.Player, req VillageLicenceRequest, now time.Time) error {
	if !has {
		return refuseVillage(village.VillageNoSettlement)
	}
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.LicenceSetKind); err != nil {
		return err
	}
	if ok, err := tx.Licences().CloseRule(ctx, s.CityID, strings.TrimSpace(req.Code), now); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.LicenceNoRule, village.AddrLicence)
	}
	return h.licenceAudit(ctx, tx, s.CityID, "", p.ID, "kind_closed", map[string]any{"kind": strings.TrimSpace(req.Code)}, now)
}

// rightCheck is what every grant asks: the target of an exclusive right is named and free, and the holder does not pass the share cap.
func (h *VillageHandler) rightCheck(ctx context.Context, tx application.Tx, def content.LicenceKindDef, issuerID, target, holderKind, holderID string) error {
	if def.Exclusive() {
		if target == "" || len(target) > 64 {
			return refuseVillage(village.LicenceNoTarget, village.AddrLicence)
		}
		if held, err := tx.Licences().Held(ctx, issuerID, def.Code, target); err != nil {
			return err
		} else if held != nil {
			return refuseVillage(village.LicenceTaken, village.AddrLicence)
		}
	} else if target != "" {
		return refuseVillage(village.LicenceBadArgs, village.AddrLicence)
	}
	return h.shareCheck(ctx, tx, issuerID, def.Code, holderKind, holderID)
}

func (h *VillageHandler) shareCheck(ctx context.Context, tx application.Tx, issuerID, kind, holderKind, holderID string) error {
	total, err := tx.Licences().CountLive(ctx, issuerID, kind)
	if err != nil {
		return err
	}
	held, err := tx.Licences().CountHeld(ctx, issuerID, kind, holderKind, holderID)
	if err != nil {
		return err
	}
	if h.licenceRules().OverShare(held, total) {
		return refuseVillage(village.LicenceOverShare, village.AddrLicence)
	}
	return nil
}

// grant writes a licence and puts the end of its term on the clock.
func (h *VillageHandler) grantLicence(ctx context.Context, tx application.Tx, id, kind, issuerID, holderKind, holderID, target, via string, paid int64, transferable bool,
	start, end time.Time, from string, now time.Time,
) (*application.Licence, error) {
	l := application.Licence{ID: id, Kind: kind, IssuerID: issuerID, HolderKind: holderKind, HolderID: holderID, TargetRef: target, Via: via, Paid: paid, Transferable: transferable,
		Status: "active", TermStart: start, TermEnd: end, CreatedAt: now, FromLicence: from}
	ok, err := tx.Licences().Insert(ctx, l)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, refuseVillage(village.LicenceTaken, village.AddrLicence)
	}
	if err := h.schedulePolity(ctx, tx, application.TickLicenceExpiry, application.LicenceReference, l.ID, "", holderID, now, end); err != nil {
		return nil, err
	}
	return &l, nil
}

// licenceClaim is the first grant in the grace: whoever is already at work gets the right at the posted fee (or the reserve), no auction.
func (h *VillageHandler) licenceClaim(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, has bool, p *application.Player,
	req VillageLicenceRequest, now time.Time,
) error {
	if !has {
		return refuseVillage(village.VillageNoSettlement)
	}
	def, ok := snap.LicenceKind(strings.TrimSpace(req.Code))
	if !ok {
		return refuseVillage(village.LicenceNoKind, village.AddrLicence)
	}
	rule, err := tx.Licences().Rule(ctx, s.CityID, def.Code)
	if err != nil {
		return err
	}
	if rule == nil {
		return refuseVillage(village.LicenceNoRule, village.AddrLicence)
	}
	req0 := requirementOf(*rule)
	if req0.InForce(now) {
		return refuseVillage(village.LicenceGraceOver, village.AddrLicence)
	}
	hk, hid, err := h.holderOf(ctx, tx, p, req.Company)
	if err != nil {
		return err
	}
	target := strings.TrimSpace(req.Target)
	if err := h.rightCheck(ctx, tx, def, s.CityID, target, hk, hid); err != nil {
		return err
	}
	price := req0.Price()
	if price > 0 {
		from, err := holderAccount(ctx, tx, hk, hid)
		if err != nil {
			return err
		}
		if bal, err := accountBalance(ctx, tx, from); err != nil {
			return err
		} else if bal < price {
			return refuseVillage(village.LicenceNoMoney, village.AddrLicence)
		}
		treasury, err := cityTreasury(ctx, tx, s.CityID)
		if err != nil {
			return err
		}
		if _, err := move(ctx, tx, from.ID, treasury.ID, price, application.ReasonLicenceIssueFee, application.LicenceReference, s.CityID, "", now); err != nil {
			return err
		}
	}
	end := now.Add(req0.Term)
	l, err := h.grantLicence(ctx, tx, h.ids.NewID(), def.Code, s.CityID, hk, hid, target, "first_grant", price, rule.Transferable, now, end, "", now)
	if err != nil {
		return err
	}
	return h.licenceAudit(ctx, tx, s.CityID, l.ID, p.ID, "granted", map[string]any{"kind": def.Code, "via": "first_grant", "holder_kind": hk, "price": price}, now)
}
