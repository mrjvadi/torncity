package handlers

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The settlement's own insurance fund (docs/adr/0074 5.2), a friendly society: the holder of insurance.run opens it and sets how dear its
// cover is within bounds; residents buy the fund's products and pay their premiums into it; a fall or a raid is paid out of it, pro
// rata when it is short. The fund is an insurance_fund account owned by the settlement; the policies, the premium rounds and the claims
// are those of the finance tables (migration 0152 lets an issuer be a settlement). The neutral city's fund (insure.list) stays the
// fallback for those whose settlement keeps none.

// VillageInsuranceRequest is the payload of settlement.insurance.
type VillageInsuranceRequest struct {
	// Action is "" (the screen), open, scale or buy (village.InsuranceAction*).
	Action string `json:"action,omitempty"`
	// Code is a product's code (buy); N the price level in basis points (scale) or the building to insure (buy).
	Code string `json:"code,omitempty"`
	N    string `json:"n,omitempty"`
}

// Insurance handles settlement.insurance.
func (h *VillageHandler) Insurance(ctx context.Context, meta envelope.Metadata, req VillageInsuranceRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.SettlementInsuranceView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
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
				if notice, err = h.insuranceAct(ctx, tx, meta, snap, s, p, act, req); err != nil {
					return err
				}
			}
		}
		view, err = h.insuranceView(ctx, tx, snap, s, p)
		view.Notice = notice
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.Insurance(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) insuranceAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement,
	p *application.Player, act string, req VillageInsuranceRequest,
) (string, error) {
	now := h.now()
	ins, err := tx.Raids().Insurer(ctx, s.CityID)
	if err != nil {
		return "", err
	}
	switch act {
	case village.InsuranceActionOpen:
		if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.InsuranceRun); err != nil {
			return "", err
		}
		if ins != nil {
			return "", refuseVillage(village.InsuranceAlready, village.AddrInsurance)
		}
		if _, err := tx.Raids().OpenInsurer(ctx, application.SettlementInsurer{SettlementID: s.CityID, OpenedBy: p.ID, OpenedAt: now, ScaleBPS: 10_000}); err != nil {
			return "", err
		}
		if _, err := tx.Ledger().AccountFor(ctx, application.AccountInsuranceFund, s.CityID); err != nil {
			return "", err
		}
		return village.InsuranceNoticeOpened, appendVillageEvent(ctx, tx, meta, "insurance_opened", s.CityID, map[string]any{"settlement_id": s.CityID, "opened_by": p.ID})
	case village.InsuranceActionScale:
		if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.InsuranceRun); err != nil {
			return "", err
		}
		if ins == nil {
			return "", refuseVillage(village.InsuranceNotOpen, village.AddrInsurance)
		}
		bps, err := strconv.ParseInt(strings.TrimSpace(req.N), 10, 64)
		if err != nil || bps < 5_000 || bps > 20_000 {
			return "", refuseVillage(village.InsuranceBadScale, village.AddrInsurance)
		}
		if err := tx.Raids().SetInsurerScale(ctx, s.CityID, bps); err != nil {
			return "", err
		}
		return village.InsuranceNoticeScaled, nil
	case village.InsuranceActionBuy:
		return village.InsuranceNoticeBought, h.insuranceBuy(ctx, tx, snap, s, p, ins, req, now)
	}
	return "", refuseVillage(village.RaidBadArgs, village.AddrInsurance)
}

func (h *VillageHandler) insuranceBuy(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, p *application.Player,
	ins *application.SettlementInsurer, req VillageInsuranceRequest, now time.Time,
) error {
	if ins == nil {
		return refuseVillage(village.InsuranceNotOpen, village.AddrInsurance)
	}
	home, err := tx.Employment().ResidenceCityID(ctx, p.ID)
	if err != nil {
		return err
	}
	if home != s.CityID {
		return refuseVillage(village.RaidNotResident, village.AddrInsurance)
	}
	def, ok := snap.Finance()
	if !ok {
		return refuseVillage(village.InsuranceNoProduct, village.AddrInsurance)
	}
	product, ok := def.InsuranceProduct(strings.TrimSpace(req.Code))
	if !ok || product.IssuerOrCountry() != content.IssuerSettlement {
		return refuseVillage(village.InsuranceNoProduct, village.AddrInsurance)
	}
	pol := application.InsurancePolicy{ID: h.ids.NewID(), PlayerID: p.ID, Product: product.Code, Covers: product.Covers, CountryID: s.CityID,
		IssuerKind: content.IssuerSettlement, StartedAt: now, ClaimsFrom: now.Add(h.raidScale.RealWait(product.WaitingDuration()))}
	if product.Covers == content.CoverRaidDamage {
		b, err := tx.SettlementBuildings().Get(ctx, strings.TrimSpace(req.N))
		if err != nil || b == nil || b.SettlementID != s.CityID || b.Status != "complete" || b.EmployerPlayerID != p.ID {
			return refuseVillage(village.InsuranceNoBuilding, village.AddrInsurance)
		}
		pol.BuildingID = b.ID
	}
	amount, _, err := premiumOf(ctx, tx, snap, product, pol)
	if err != nil {
		return err
	}
	pu, err := playerPurse(ctx, tx, p.ID)
	if err != nil {
		return err
	}
	if pu.total() < amount {
		return refuseVillage(village.InsuranceNoMoney, village.AddrInsurance)
	}
	if pol, err = tx.Finance().CreatePolicy(ctx, pol); err != nil {
		if isSentinel(err, application.ErrPolicyExists) {
			return refuseVillage(village.InsuranceHeld, village.AddrInsurance)
		}
		return err
	}
	fund, err := tx.Ledger().AccountFor(ctx, application.AccountInsuranceFund, s.CityID)
	if err != nil {
		return err
	}
	period, _, err := tx.Finance().CurrentPeriod(ctx)
	if err != nil {
		return err
	}
	txID := h.ids.NewID()
	if _, err := tx.Finance().RecordPremium(ctx, pol.ID, period, amount, txID, now); err != nil {
		return err
	}
	_, err = pu.pay(ctx, tx, amount, application.ReasonInsurancePremium, application.InsurancePolicyReference, pol.ID, fund.ID, txID, now)
	return err
}

func (h *VillageHandler) insuranceView(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, p *application.Player) (village.SettlementInsuranceView, error) {
	v := village.SettlementInsuranceView{Village: s.Name, MayRun: hasPermission(ctx, tx, s, p.ID, charter.InsuranceRun)}
	ins, err := tx.Raids().Insurer(ctx, s.CityID)
	if err != nil {
		return v, err
	}
	if ins == nil {
		return v, nil
	}
	v.Open, v.ScaleBPS = true, ins.ScaleBPS
	fund, err := tx.Ledger().AccountFor(ctx, application.AccountInsuranceFund, s.CityID)
	if err != nil {
		return v, err
	}
	v.Fund = fund.Balance.Minor()
	def, ok := snap.Finance()
	if !ok {
		return v, nil
	}
	pols, err := tx.Finance().PoliciesOf(ctx, p.ID, 50)
	if err != nil {
		return v, err
	}
	held := map[string]bool{}
	for _, pol := range pols {
		if pol.IssuerKind != content.IssuerSettlement || pol.CountryID != s.CityID {
			continue
		}
		prod, _ := def.InsuranceProduct(pol.Product)
		line := village.SettlementInsurancePolicyLine{No: pol.No, Product: presentation.Named{Code: pol.Product, Name: prod.Name}, Covers: pol.Covers, Status: pol.Status}
		if pol.BuildingID != "" {
			line.Building = &presentation.Named{Code: pol.BuildingID, Name: pol.BuildingID}
		}
		if line.Paid, err = tx.Finance().ClaimsPaid(ctx, pol.ID); err != nil {
			return v, err
		}
		if pol.Status == application.PolicyActive {
			held[pol.Product+"/"+pol.BuildingID] = true
		}
		v.Policies = append(v.Policies, line)
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return v, err
	}
	for _, d := range def.Insurance {
		if d.IssuerOrCountry() != content.IssuerSettlement {
			continue
		}
		line := village.SettlementInsuranceProductLine{Product: presentation.Named{Code: d.Code, Name: d.Name}, Covers: d.Covers,
			Premium: max(d.Premium*ins.ScaleBPS/10_000, 1), PremBPS: d.PremiumBPS * ins.ScaleBPS / 10_000, CoverBPS: d.CoverBPS, DeductibleBPS: d.DeductibleBPS,
			MaxClaim: d.MaxClaim, Waiting: h.raidScale.RealWait(d.WaitingDuration()), Held: held[d.Code+"/"]}
		if d.Covers == content.CoverRaidDamage {
			for _, b := range buildings {
				if b.Status == "complete" && b.EmployerPlayerID == p.ID && !held[d.Code+"/"+b.ID] {
					line.Targets = append(line.Targets, presentation.Named{Code: b.ID, Name: b.TypeCode})
				}
			}
		}
		v.Products = append(v.Products, line)
	}
	return v, nil
}
