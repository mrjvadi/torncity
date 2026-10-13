package handlers

import (
	"context"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/law"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// lawServices are the daily services the item uses, with the building that gives each.
var lawServices = []struct{ Service, Building string }{
	{"constabulary", "constable_post"}, {"court", "courthouse"}, {"custody", "jail"}, {"customs", "customs_house"},
}

// lawNamed is a player as the screens name him.
func lawNamed(ctx context.Context, tx application.Tx, cache map[string]presentation.Named, id string) presentation.Named {
	if n, ok := cache[id]; ok {
		return n
	}
	n := presentation.Named{}
	if p, err := tx.Players().GetByID(ctx, id); err == nil && p != nil {
		n = presentation.Named{Code: p.PublicCode, Name: p.DisplayName}
	}
	cache[id] = n
	return n
}

// lawView builds the law screen for the viewer.
func (h *VillageHandler) lawView(ctx context.Context, tx application.Tx, lc *lawCtx) (village.LawView, error) {
	now := lc.now
	v := village.LawView{Mature: lc.mature, Open: village.LawOpen{Police: h.policeOpen(), Courts: h.courtsOpen(), Black: h.blackOpen(), Customs: h.customsOpen(), Launder: h.launderOpen()}}
	if !lc.has {
		return v, nil
	}
	v.Village = lc.s.Name
	var err error
	if v.Model, err = tx.Law().Model(ctx, lc.s.CityID); err != nil {
		return v, err
	}
	st, err := loadCharterState(ctx, tx, lc.s, "head")
	if err != nil {
		return v, err
	}
	held := st.heldBy(lc.p.ID)
	may := func(p charter.Permission) bool { _, ok := held.Has(p); return ok }
	v.Mays = village.LawMays{Set: may(charter.LawSet), Patrol: may(charter.PolicePatrol), Investigate: may(charter.PoliceInvestigate), Arrest: may(charter.PoliceArrest),
		WarrantAsk: may(charter.PoliceWarrantAsk), WarrantSign: may(charter.CourtWarrant), Charge: may(charter.ProsecuteCharge), Drop: may(charter.ProsecuteDrop),
		Defence: may(charter.DefenceAppear), Judge: may(charter.CourtJudge), AppealHear: may(charter.CourtAppealHear), Pardon: may(charter.Pardon),
		Informants: may(charter.PoliceInformantRun), AuditIntegrity: may(charter.AuditIntegrity), AuditBooks: may(charter.AuditBooks)}
	staff := v.Mays.Patrol || v.Mays.Investigate || v.Mays.Arrest || v.Mays.Charge || v.Mays.Judge || v.Mays.Drop || v.Mays.AppealHear || v.Mays.Set

	// the posts
	buildings, err := tx.SettlementBuildings().List(ctx, lc.s.CityID)
	if err != nil {
		return v, err
	}
	day, err := h.SettleServiceDay(ctx, tx, lc.snap, lc.s, buildings)
	if err != nil {
		return v, err
	}
	posts := servicePosts(lc.snap, buildings)
	for _, svc := range lawServices {
		line := village.LawPostLine{Service: svc.Service}
		if d, ok := lc.snap.SettlementBuildingDef(svc.Building); ok {
			line.Building = presentation.Named{Code: svc.Building, Name: d.Name}
		}
		for _, sp := range posts {
			if sp.service() != svc.Service {
				continue
			}
			line.Stands = true
			if day != nil && day.HeldPost(sp.b.ID) {
				line.Open = true
				if svc.Service == "custody" {
					var warders int64
					for _, s := range sp.def.Staff {
						if s.Role == "warder" {
							warders += int64(s.Slots)
						}
					}
					line.Cells += lc.t.JailCells(warders)
					n, err := tx.Law().JailsHeld(ctx, sp.b.ID)
					if err != nil {
						return v, err
					}
					line.Held += n
				}
			}
		}
		v.Posts = append(v.Posts, line)
	}
	if v.Open.Police {
		for _, p := range v.Posts {
			if p.Service == "constabulary" && p.Open {
				_, _, _, err = h.lawPoints(ctx, tx, lc)
				if err != nil {
					return v, err
				}
			}
		}
		if p, sp, pat, ok, err := tx.Law().Points(ctx, lc.s.CityID, h.lawDay(lc)); err != nil {
			return v, err
		} else if ok {
			v.Points, v.Spent, v.Patrol = p, sp, pat
		}
	}

	// the offences
	listed, err := tx.Law().Offences(ctx, lc.s.CityID)
	if err != nil {
		return v, err
	}
	byCode := map[string]application.LawOffence{}
	for _, o := range listed {
		byCode[o.Offence] = o
	}
	for _, d := range lc.snap.Offences() {
		if lawMatureHidden(lc.mature, d.Mature) {
			continue
		}
		line := village.LawOffenceLine{Code: d.Code, Name: d.Name, Class: d.Class, Sentences: d.Sentences, MaxFine: d.MaxFine, MaxJailHours: d.MaxJailHours, MaxBanDays: d.MaxBanDays}
		if o, ok := byCode[d.Code]; ok {
			line.Listed, line.MaxFine, line.MaxJailHours, line.MaxBanDays, line.EffectiveAt = true, o.MaxFine, o.MaxJailHours, o.MaxBanDays, o.EffectiveAt
		}
		if missing, err := knowledgeMissing(ctx, tx, lc.s.CityID, needsKnowledge(d.Requires)); err != nil {
			return v, err
		} else {
			line.Available = len(missing) == 0
		}
		v.Offences = append(v.Offences, line)
	}

	names := map[string]presentation.Named{}
	caseLine := func(c application.LawCase) (village.LawCaseLine, error) {
		def, _ := lc.snap.Offence(c.Offence)
		items, err := tx.Law().Evidence(ctx, c.ID)
		if err != nil {
			return village.LawCaseLine{}, err
		}
		return village.LawCaseLine{ID: c.ID, No: c.No, Offence: presentation.Named{Code: c.Offence, Name: def.Name}, Accused: lawNamed(ctx, tx, names, c.AccusedID),
			State: c.State, Source: c.Source, Strength: lc.t.Total(lawEvidenceItems(items), false), Custody: c.CustodyUntil != nil, CustodyUntil: c.CustodyUntil, At: c.CreatedAt}, nil
	}

	// the cases: all of them for those who work the law, the viewer's own for everyone else
	var cases []application.LawCase
	if staff || v.Mays.WarrantSign || v.Mays.Defence {
		cases, err = tx.Law().CasesOf(ctx, lc.s.CityID, 30)
	} else {
		var mine []application.LawCase
		mine, err = tx.Law().CasesAgainst(ctx, lc.p.ID, 30)
		for _, c := range mine {
			if c.SettlementID == lc.s.CityID {
				cases = append(cases, c)
			}
		}
	}
	if err != nil {
		return v, err
	}
	for _, c := range cases {
		ln, err := caseLine(c)
		if err != nil {
			return v, err
		}
		v.Cases = append(v.Cases, ln)
	}
	if lc.req.ID != "" {
		if c, err := tx.Law().CaseByID(ctx, lc.req.ID, false); err != nil {
			return v, err
		} else if c != nil && c.SettlementID == lc.s.CityID && (staff || v.Mays.WarrantSign || v.Mays.Defence || c.AccusedID == lc.p.ID) {
			d, err := h.lawCaseDetail(ctx, tx, lc, v.Mays, c, caseLine)
			if err != nil {
				return v, err
			}
			v.Case = d
		}
	}

	// the viewer's own sentences in this settlement
	own, err := tx.Law().CasesAgainst(ctx, lc.p.ID, 10)
	if err != nil {
		return v, err
	}
	for _, c := range own {
		if c.SettlementID != lc.s.CityID {
			continue
		}
		ss, err := tx.Law().SentencesOf(ctx, c.ID)
		if err != nil {
			return v, err
		}
		for _, s := range ss {
			if s.PlayerID == lc.p.ID {
				v.Mine = append(v.Mine, lawSentenceLine(lc, s, v.Mays.Pardon, now))
			}
		}
	}

	// informants: the handler's own, and every one for the integrity auditor
	if v.Mays.Informants || v.Mays.AuditIntegrity {
		hid := lc.p.ID
		if v.Mays.AuditIntegrity {
			hid = ""
		}
		infs, err := tx.Law().InformantsOf(ctx, lc.s.CityID, hid)
		if err != nil {
			return v, err
		}
		for _, i := range infs {
			ln := village.LawInformantLine{ID: i.ID, Reliability: i.Reliability, State: i.State, Mine: i.HandlerID == lc.p.ID, Since: i.HandlerSince,
				RotateDue: lc.t.NeedsRotation(int64(now.Sub(i.HandlerSince) / time.Hour))}
			if ln.Mine && i.SourceID != "" {
				ln.Source = lawNamed(ctx, tx, names, i.SourceID)
			}
			v.Informants = append(v.Informants, ln)
		}
	}
	// bribes: the offers the viewer made or was made
	bribes, err := tx.Law().BribesOf(ctx, lc.s.CityID, 60)
	if err != nil {
		return v, err
	}
	for _, b := range bribes {
		if b.BriberID != lc.p.ID && b.HolderID != lc.p.ID {
			continue
		}
		v.Bribes = append(v.Bribes, village.LawBribeLine{ID: b.ID, Rung: b.Rung, Status: b.Status, EffectBPS: b.EffectBPS, Amount: b.Amount, Dirty: b.Dirty,
			Mine: b.BriberID == lc.p.ID, MayAnswer: b.HolderID == lc.p.ID && b.Status == "offered", TermEnd: b.TermEnd})
	}
	if v.Captured, err = h.lawCaptured(ctx, tx, lc); err != nil {
		return v, err
	}

	// the hidden board
	if v.Open.Black {
		if err := h.lawBoardView(ctx, tx, lc, &v); err != nil {
			return v, err
		}
	}
	// fronts and loads
	if v.Open.Launder {
		fronts, err := tx.Law().FrontsOf(ctx, lc.p.ID)
		if err != nil {
			return v, err
		}
		for _, f := range fronts {
			if f.SettlementID != lc.s.CityID {
				continue
			}
			co, err := tx.Companies().ByID(ctx, f.CompanyID)
			if err != nil || co == nil {
				continue
			}
			ln := village.LawFrontLine{Company: presentation.Named{Code: co.Code, Name: co.Name}}
			bs, err := tx.Law().BatchesOf(ctx, f.CompanyID, h.lawDay(lc)-lc.t.LaunderWindowDays)
			if err != nil {
				return v, err
			}
			for _, b := range bs {
				ln.Batches = append(ln.Batches, village.LawBatchLine{ID: b.ID, State: b.State, Amount: b.Amount, StageDue: b.StageDue})
			}
			v.Fronts = append(v.Fronts, ln)
		}
	}
	if v.Open.Customs {
		loads, err := tx.Law().LoadsOf(ctx, lc.p.ID, 10)
		if err != nil {
			return v, err
		}
		for _, l := range loads {
			to := presentation.Named{}
			if s, err := tx.Settlements().ByID(ctx, l.ToID); err == nil {
				to = presentation.Named{Code: s.Code, Name: s.Name}
			}
			v.Loads = append(v.Loads, village.LawLoadLine{ID: l.ID, Status: l.Status, To: to, Declared: l.Declared, Carried: l.Carried, Inspected: l.Inspected,
				Duty: l.Duty, Penalty: l.Penalty, MayCross: l.Status == "declared"})
		}
	}
	if err := h.lawChoices(ctx, tx, lc, &v); err != nil {
		return v, err
	}
	return v, nil
}

// lawChoices fills the lists a player picks from, so no code is ever typed: his goods, the destinations, his businesses, the brokers and his gang.
func (h *VillageHandler) lawChoices(ctx context.Context, tx application.Tx, lc *lawCtx, v *village.LawView) error {
	if v.Open.Customs || v.Board.Known {
		stacks, pieces, err := tx.Items().Holdings(ctx, lc.p.ID, application.HoldCarried)
		if err != nil {
			return err
		}
		held := map[string]int64{}
		for _, st := range stacks {
			held[st.Item] += st.Qty
		}
		for _, pc := range pieces {
			held[pc.Item]++
		}
		for it, q := range held {
			if q < 1 {
				continue
			}
			v.Goods = append(v.Goods, village.LawGoodLine{Item: presentation.Named{Code: it, Name: snapItemName(lc.snap, it)}, Qty: q})
		}
		sort.Slice(v.Goods, func(i, j int) bool { return v.Goods[i].Item.Code < v.Goods[j].Item.Code })
	}
	if v.Open.Customs {
		founded, err := tx.Settlements().Founded(ctx)
		if err != nil {
			return err
		}
		for _, f := range founded {
			if f.CityID != lc.s.CityID {
				v.Destinations = append(v.Destinations, presentation.Named{Code: f.Code, Name: f.Name})
			}
		}
		sort.Slice(v.Destinations, func(i, j int) bool { return v.Destinations[i].Name < v.Destinations[j].Name })
	}
	if v.Open.Launder {
		cos, err := tx.Companies().Of(ctx, lc.p.ID)
		if err != nil {
			return err
		}
		for _, co := range cos {
			if co.OwnerID != lc.p.ID || co.CityID != lc.s.CityID || co.Status != application.CompanyActive {
				continue
			}
			if f, err := tx.Law().FrontByCompany(ctx, co.ID); err != nil {
				return err
			} else if f != nil {
				continue
			}
			v.Companies = append(v.Companies, presentation.Named{Code: co.Code, Name: co.Name})
		}
	}
	people, err := tx.Law().Residents(ctx, lc.s.CityID, 300)
	if err != nil {
		return err
	}
	for _, pr := range people {
		if pr.ID != lc.p.ID {
			v.People = append(v.People, presentation.Named{Code: pr.Code, Name: pr.Name})
		}
	}
	if v.Open.Black || v.Mays.AuditIntegrity {
		if err := h.lawHolderLines(ctx, tx, lc, v); err != nil {
			return err
		}
	}
	if v.Board.Known {
		bs, err := tx.Law().Brokers(ctx, 30)
		if err != nil {
			return err
		}
		names := map[string]presentation.Named{}
		for _, b := range bs {
			if b.PlayerID == lc.p.ID {
				continue
			}
			v.Board.Brokers = append(v.Board.Brokers, village.LawBrokerLine{Player: lawNamed(ctx, tx, names, b.PlayerID), Reputation: b.Reputation, Cap: lc.t.BrokerCap(b.Reputation)})
		}
	}
	// the gang: its leader knows it; a member sees it only once it has been declared in the open
	if v.Open.Courts {
		if m, err := tx.Factions().Membership(ctx, lc.p.ID); err == nil && m != nil {
			if f, err := tx.Factions().ByID(ctx, m.FactionID); err == nil && f != nil {
				g, err := tx.Law().Gang(ctx, f.ID)
				if err != nil {
					return err
				}
				leader := f.LeaderID == lc.p.ID
				if g != nil && (leader || !g.Hidden) {
					ln := &village.LawGangLine{Faction: presentation.Named{Code: f.Code, Name: f.Name}, Hidden: g.Hidden, Leader: leader}
					if leader {
						ln.Heat = g.Heat
					}
					v.Gang = ln
				} else if g == nil && leader {
					v.Gang = &village.LawGangLine{Faction: presentation.Named{Code: f.Code, Name: f.Name}, Hidden: true, Leader: true}
				}
			}
		}
	}
	return nil
}

func lawSentenceLine(lc *lawCtx, s application.LawSentence, mayPardon bool, now time.Time) village.LawSentenceLine {
	ln := village.LawSentenceLine{ID: s.ID, Kind: s.Kind, Status: s.Status, Ban: s.Ban, Fine: s.Fine, FinePaid: s.FinePaid, JailHours: s.JailHours, BanDays: s.BanDays,
		Cell: s.Cell, EndsAt: s.EndsAt, MayPardon: mayPardon && s.Status == "active"}
	if s.Kind == "jail" && s.Status == "active" && s.EndsAt != nil {
		ln.Bail = lc.t.Bail(max(int64(s.EndsAt.Sub(now)/time.Hour)+1, 1))
		ln.MayBail = true
	}
	return ln
}

// lawCaseDetail is a case opened on the screen, with what the viewer may do to it now.
func (h *VillageHandler) lawCaseDetail(ctx context.Context, tx application.Tx, lc *lawCtx, mays village.LawMays, c *application.LawCase,
	caseLine func(application.LawCase) (village.LawCaseLine, error),
) (*village.LawCaseDetail, error) {
	ln, err := caseLine(*c)
	if err != nil {
		return nil, err
	}
	d := &village.LawCaseDetail{Line: ln, Model: c.Model}
	items, err := tx.Law().Evidence(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	for _, e := range items {
		d.Evidence = append(d.Evidence, village.LawEvidenceLine{ID: e.ID, Kind: e.Kind, Strength: e.Strength, Proper: e.Proper, Excluded: e.Excluded})
	}
	ev := lawEvidenceItems(items)
	d.Total, d.Quality = lc.t.Total(ev, false), lc.t.Quality(ev)
	ws, err := tx.Law().WarrantsOf(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	for _, w := range ws {
		d.Warrants = append(d.Warrants, w.ID+":"+w.Status)
	}
	for _, inst := range []int{1, 2} {
		v, err := tx.Law().VerdictOf(ctx, c.ID, inst)
		if err != nil {
			return nil, err
		}
		if v != nil {
			d.Verdicts = append(d.Verdicts, village.LawVerdictLine{Instance: inst, Guilty: v.Guilty, NPC: v.NPC, Defence: v.Defence, Total: v.Total, Effective: v.Effective,
				Quality: v.Quality, At: v.At})
		}
	}
	ss, err := tx.Law().SentencesOf(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	for _, s := range ss {
		d.Sentences = append(d.Sentences, lawSentenceLine(lc, s, mays.Pardon, lc.now))
	}
	as, err := tx.Law().AppealsOf(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	for _, a := range as {
		d.Appeals = append(d.Appeals, a.Party+":"+a.Status)
	}
	me := lc.p.ID
	open := c.State == "open" || c.State == "charged"
	d.MayInvestigate = mays.Investigate && open
	d.MayAskWarrant = mays.WarrantAsk && open
	d.MaySign = mays.WarrantSign && open && !law.Conflict(c.ChargedBy, me)
	d.MayArrest = mays.Arrest && open && c.CustodyUntil == nil
	d.MayCharge = mays.Charge && c.State == "open" && c.AccusedID != me
	d.MayDrop = mays.Drop && open
	d.MayDefend = (c.AccusedID == me && c.DefenceID == "" && !c.DefenceNPC && open) || (mays.Defence && c.DefenceID == "" && !c.DefenceNPC && open && c.ChargedBy != me)
	d.MayVerdict = mays.Judge && c.State == "hearing" && c.AccusedID != me && !(c.Model == "adversarial" && law.Conflict(c.ChargedBy, me))
	if c.AccusedID == me && (c.State == "decided" || c.State == "appealed") {
		d.MayAppeal, d.MayAppealAs = true, "accused"
	} else if mays.Charge && (c.State == "decided" || c.State == "appealed") {
		d.MayAppeal, d.MayAppealAs = true, "prosecution"
	}
	if first, _ := tx.Law().VerdictOf(ctx, c.ID, 1); first != nil {
		d.MayDecide = mays.AppealHear && c.State == "appealed" && me != first.JudgeID && me != c.ChargedBy && me != c.AccusedID
	}
	return d, nil
}

// lawBoardView fills the hidden board for a viewer who has standing; for anyone else it is empty, and nothing is said of what is on it.
func (h *VillageHandler) lawBoardView(ctx context.Context, tx application.Tx, lc *lawCtx, v *village.LawView) error {
	st, known, err := lawStanding(ctx, tx, lc.p.ID)
	if err != nil {
		return err
	}
	_, dirty, err := lawBalance(ctx, tx, application.AccountPlayerDirty, lc.p.ID)
	if err != nil {
		return err
	}
	v.Board = village.LawBoard{Known: known, Standing: st, Dirty: dirty}
	if !known {
		return nil
	}
	if b, err := tx.Law().Broker(ctx, lc.p.ID); err != nil {
		return err
	} else if b != nil {
		v.Board.Broker, v.Board.Rep, v.Board.BrokerCap = true, b.Reputation, lc.t.BrokerCap(b.Reputation)
	}
	ls, err := tx.Law().OpenListings(ctx, lc.s.CityID, 40)
	if err != nil {
		return err
	}
	for _, l := range ls {
		if lawMatureHidden(lc.mature, l.Mature) {
			continue
		}
		name := l.Item
		if d, ok := lc.snap.ItemDef(l.Item); ok && d.Name != "" {
			name = d.Name
		}
		ln := village.LawListingLine{ID: l.ID, Item: presentation.Named{Code: l.Item, Name: name}, Qty: l.Qty, Price: l.Price, Fence: l.SellerID == "", Mine: l.SellerID == lc.p.ID}
		if sd, ok := lc.snap.ShadowSupplyOf(l.Item); ok && l.SellerID == "" {
			ln.MinStanding = sd.MinStanding
		}
		v.Board.Listings = append(v.Board.Listings, ln)
	}
	sort.SliceStable(v.Board.Listings, func(i, j int) bool { return v.Board.Listings[i].Price < v.Board.Listings[j].Price })
	es, err := tx.Law().EscrowsOf(ctx, lc.p.ID, 20)
	if err != nil {
		return err
	}
	for _, e := range es {
		l, err := tx.Law().ListingByID(ctx, e.ListingID, false)
		if err != nil || l == nil {
			continue
		}
		name := l.Item
		if d, ok := lc.snap.ItemDef(l.Item); ok && d.Name != "" {
			name = d.Name
		}
		v.Board.Escrows = append(v.Board.Escrows, village.LawEscrowLine{ID: e.ID, Status: e.Status, Amount: e.Amount, Fee: e.Fee, Qty: e.Qty,
			Item: presentation.Named{Code: l.Item, Name: name}, Buyer: e.BuyerID == lc.p.ID, Broker: e.BrokerID == lc.p.ID, Mine: l.SellerID == lc.p.ID, DueAt: e.DueAt})
	}
	return nil
}

// lawHolderLines lists the holders of the offices that decide the rungs of the law, and of the offices that can be audited (every office that holds
// a power of the police or the courts), each with the office's title.
func (h *VillageHandler) lawHolderLines(ctx context.Context, tx application.Tx, lc *lawCtx, v *village.LawView) error {
	st, err := loadCharterState(ctx, tx, lc.s, "head")
	if err != nil {
		return err
	}
	lawPerms := []charter.Permission{charter.PolicePatrol, charter.PoliceInvestigate, charter.PoliceArrest, charter.CourtWarrant, charter.CourtJudge,
		charter.CourtAppealHear, charter.ProsecuteCharge}
	names := map[string]presentation.Named{}
	for _, o := range st.offices {
		if o.Closed {
			continue
		}
		var rungs []string
		isLaw := false
		for _, g := range o.Grants {
			for _, p := range lawPerms {
				isLaw = isLaw || g.Permission == p
			}
			for r, p := range bribeRungs {
				if g.Permission == p {
					rungs = append(rungs, r)
				}
			}
		}
		if !isLaw {
			continue
		}
		sort.Strings(rungs)
		var holders []string
		if o.Acquisition == charter.AcquireHead {
			if st.headHeld != "" {
				holders = append(holders, st.headHeld)
			}
		} else {
			for _, seat := range st.seats[o.ID] {
				holders = append(holders, seat.HolderID)
			}
		}
		for _, id := range holders {
			if id == lc.p.ID {
				continue
			}
			v.Holders = append(v.Holders, village.LawHolderLine{Player: lawNamed(ctx, tx, names, id), Office: o.Title, Rungs: rungs})
		}
	}
	return nil
}
