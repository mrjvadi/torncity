package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The charter (docs/adr/0044 section 6): the offices the players of a settlement
// create, the atomic permissions each holds, who sits in them, and the rails no
// charter can change. Every act of the village asks `requireVillage` for ONE
// permission instead of "are you the head".
//
// DEFAULT. A settlement with no rows has, in memory, one founder's office whose
// holder is whoever holds the head office in the governance seats (so the existing
// succession and elections keep working) and which holds every permission with no
// limit: nothing a live head could do yesterday is lost. The first edit writes
// that office down. The founder's office cannot be closed and keeps its manager
// powers; its title is the players' to change (the default word is a locale string).
//
// RAILS. R1 nobody grants what they do not hold (a grant or an appointment must be
// covered by the actor's own offices); R2 an office manager who can act always
// exists; R3 every change goes to the append-only charter_audit; R4 sizes are
// capped (config settlement.charter_*). The election of offices, recall by the
// residents and the amendment vote (R5-R10, ADR 0044 6.5/6.6) are the next phase.

// charterDefaults are the caps used when VillageRules carries none.
func charterDefaults() charter.Limits {
	return charter.Limits{MaxOffices: 24, MaxSeats: 15, MaxPermissions: 40, TitleMin: 2, TitleMax: 32}
}

func (h *VillageHandler) limits() charter.Limits {
	l := h.charterLimits
	if l.MaxOffices == 0 {
		l = charterDefaults()
	}
	if snap := h.content.Current(); snap != nil {
		if f, ok := snap.Founding(); ok {
			l.Reserved = f.ReservedNames
		}
	}
	return l
}

// FounderOfficeID is the id of the founder's office in the view while nothing is
// written: save, close and appoint recognise it and write the charter down first, so
// the founder can rename their title from the very first edit.
const FounderOfficeID = "founder"

// charterState is a settlement's charter as it stands: the open offices (the
// default one when nothing is written), the active seats and whether the head
// office has a holder.
type charterState struct {
	offices   []charter.Office
	seats     map[string][]application.CharterSeat // office id -> seats
	stored    bool
	headHeld  string // player id holding the governance head office, "" if none
	headIsSet bool
	// headSince is when the head seat took its present state (for a vacancy: when
	// it fell vacant); acting is who acts for the head while it is vacant.
	headSince time.Time
	acting    *actingHead
}

// actingHead is the officer who runs the settlement while its head seat is vacant
// (ADR 0044 6.5, research note section 1.5): the deputy office's longest-serving
// holder, else the longest-serving holder of any office. Their powers are the
// founder's less the ones that change the charter, capped on spending, and last at
// most charter.acting_days from the vacancy; after that nothing they do is valid.
type actingHead struct {
	Player   string
	OfficeID string
	Ends     time.Time
	Grants   []charter.Grant
}

// charterRuntime is the numbers and the clock the package-level checks (requirePermission
// and its callers in other handlers) need. NewVillageHandler sets them.
var (
	charterSet atomic.Pointer[charter.Settings]
	charterNow atomic.Pointer[func() time.Time]
)

func settingsNow() (charter.Settings, time.Time) {
	set := charter.Defaults()
	if p := charterSet.Load(); p != nil {
		set = *p
	}
	now := time.Now().UTC()
	if f := charterNow.Load(); f != nil {
		now = (*f)()
	}
	return set, now
}

func (s charterState) held(o charter.Office) bool {
	if o.Acquisition == charter.AcquireHead {
		return s.headHeld != ""
	}
	return len(s.seats[o.ID]) > 0
}

// loadCharter reads the charter. It never writes: the default office has an empty
// id until the first edit stores it.
func (h *VillageHandler) loadCharter(ctx context.Context, tx application.Tx, s application.FoundedSettlement, lang string) (charterState, error) {
	return loadCharterState(ctx, tx, s, h.defaultTitle(lang))
}

// loadCharterState is loadCharter for any handler: title names the default
// founder's office while nothing is written.
func loadCharterState(ctx context.Context, tx application.Tx, s application.FoundedSettlement, title string) (charterState, error) {
	st := charterState{seats: map[string][]application.CharterSeat{}}
	var err error
	if st.offices, err = tx.Charters().Offices(ctx, s.CityID); err != nil {
		return st, err
	}
	if len(st.offices) == 0 {
		st.offices = []charter.Office{charter.FoundersOffice("", title)}
	} else {
		st.stored = true
		seats, err := tx.Charters().Seats(ctx, s.CityID)
		if err != nil {
			return st, err
		}
		_, now := settingsNow()
		for _, seat := range seats {
			if !seat.TermEnds.IsZero() && !now.Before(seat.TermEnds) {
				continue // the term is over: the seat gives no powers, whether or not anyone has yet recorded it
			}
			st.seats[seat.OfficeID] = append(st.seats[seat.OfficeID], seat)
		}
	}
	seat, err := tx.Governance().Seat(ctx, officeFor(s.Tier), s.JurisdictionID, 1)
	if err != nil {
		return st, err
	}
	st.headSince = seat.Since
	if !seat.Vacant() {
		st.headHeld = seat.HolderPlayerID
	}
	st.headIsSet = true
	if st.headHeld == "" {
		set, now := settingsNow()
		st.acting = st.chooseActing(set, now)
	}
	return st, nil
}

// chooseActing picks the acting head and bounds their authority; nil when nobody can
// act or the acting days are over.
func (st charterState) chooseActing(set charter.Settings, now time.Time) *actingHead {
	holders := map[string][]charter.SeatInfo{}
	for _, o := range st.offices {
		for _, seat := range st.seats[o.ID] {
			holders[o.ID] = append(holders[o.ID], charter.SeatInfo{PlayerID: seat.HolderID, OfficeID: o.ID, Since: seat.Since})
		}
	}
	player, officeID, grants, ends, ok := charter.ActingFor(st.offices, holders, st.headSince, set, now)
	if !ok {
		return nil
	}
	return &actingHead{Player: player, OfficeID: officeID, Ends: ends, Grants: grants}
}

func (h *VillageHandler) defaultTitle(lang string) string {
	if h.msgs != nil {
		if t := h.msgs.T(lang, "charter.default_title", nil); t != "" && t != "charter.default_title" {
			return t
		}
	}
	return "head"
}

// heldBy is what the player holds in this charter, all their offices together.
func (st charterState) heldBy(playerID string) charter.Held {
	var sets [][]charter.Grant
	for _, o := range st.offices {
		if o.Closed {
			continue
		}
		in := false
		if o.Acquisition == charter.AcquireHead {
			in = playerID != "" && st.headHeld == playerID
		} else {
			for _, seat := range st.seats[o.ID] {
				in = in || seat.HolderID == playerID
			}
		}
		if in {
			sets = append(sets, o.Grants)
		}
	}
	if st.acting != nil && st.acting.Player == playerID {
		sets = append(sets, st.acting.Grants)
	}
	return charter.HeldBy(sets...)
}

// charterHeld is what a player may do in a settlement.
func (h *VillageHandler) charterHeld(ctx context.Context, tx application.Tx, s application.FoundedSettlement, playerID, lang string) (charter.Held, error) {
	st, err := h.loadCharter(ctx, tx, s, lang)
	if err != nil {
		return nil, err
	}
	return st.heldBy(playerID), nil
}

// requireVillage is the check every act of the village makes: the player holds the
// permission through an office of the charter. It returns the permission's ceiling
// (0: none) so a limited act can enforce it. A player without it gets
// ErrNotOfficeHolder naming the permission.
func (h *VillageHandler) requireVillage(ctx context.Context, tx application.Tx, s application.FoundedSettlement, playerID string,
	perm charter.Permission,
) (int64, error) {
	return requirePermission(ctx, tx, s, playerID, perm)
}

// requirePermission is requireVillage for any handler.
func requirePermission(ctx context.Context, tx application.Tx, s application.FoundedSettlement, playerID string,
	perm charter.Permission,
) (int64, error) {
	st, err := loadCharterState(ctx, tx, s, "head")
	held := st.heldBy(playerID)
	if err != nil {
		return 0, err
	}
	limit, ok := held.Has(perm)
	if !ok {
		return 0, application.ErrNotOfficeHolder.WithDetail("office", officeFor(s.Tier)).WithDetail("permission", string(perm))
	}
	return limit, nil
}

// hasPermission is requirePermission as a flag for views: a read error counts as no.
func hasPermission(ctx context.Context, tx application.Tx, s application.FoundedSettlement, playerID string, perm charter.Permission) bool {
	_, err := requirePermission(ctx, tx, s, playerID, perm)
	return err == nil
}

// mayVillage is requireVillage as a yes or no (view flags, optional extras).
func (h *VillageHandler) mayVillage(ctx context.Context, tx application.Tx, s application.FoundedSettlement, playerID string,
	perm charter.Permission,
) (bool, error) {
	_, err := h.requireVillage(ctx, tx, s, playerID, perm)
	switch {
	case err == nil:
		return true, nil
	case stderrors.Is(err, application.ErrNotOfficeHolder):
		return false, nil
	}
	return false, err
}

// holdsAnyOffice is true when the player sits in any office of the charter: the
// old "is the head" flag of the views, now "has a say".
func (h *VillageHandler) holdsAnyOffice(ctx context.Context, tx application.Tx, s application.FoundedSettlement, playerID string) (bool, error) {
	held, err := h.charterHeld(ctx, tx, s, playerID, "")
	return len(held) > 0, err
}

// --- the acts ------------------------------------------------------------------

// VillageCharterRequest is the payload of the charter commands.
type VillageCharterRequest struct {
	// Office names an office by id (save: empty creates a new one).
	Office string `json:"office,omitempty"`
	// Title, Seats, Grants, Acquisition and TermDays are an office being saved.
	Title       string                `json:"title,omitempty"`
	Seats       CharterInt            `json:"seats,omitempty"`
	Grants      []VillageCharterGrant `json:"grants,omitempty"`
	Acquisition string                `json:"acquisition,omitempty"`
	TermDays    CharterInt            `json:"term_days,omitempty"`
	// Deputy marks the office that holds the founder's powers (less the ones an acting
	// head may not use) while the head seat is vacant.
	Deputy *bool `json:"deputy,omitempty"`
	// Player is a resident's public code (appoint, dismiss).
	Player string `json:"player,omitempty"`
}

// CharterInt reads a whole number sent as a number or as a string: a client's
// arguments reach the handler as strings, like a button's.
type CharterInt int

// UnmarshalJSON accepts 3 and "3"; an empty string is 0.
func (n *CharterInt) UnmarshalJSON(b []byte) error {
	t := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if t == "" || t == "null" {
		*n = 0
		return nil
	}
	v, err := strconv.Atoi(t)
	if err != nil {
		return err
	}
	*n = CharterInt(v)
	return nil
}

// UnmarshalJSON reads a grant sent as {"permission", "limit"} or as the string
// "permission" or "permission:limit" (the form a client's list arguments take).
func (g *VillageCharterGrant) UnmarshalJSON(b []byte) error {
	t := strings.TrimSpace(string(b))
	if strings.HasPrefix(t, "{") {
		var raw struct {
			Permission string       `json:"permission"`
			Limit      CharterInt64 `json:"limit"`
		}
		if err := json.Unmarshal(b, &raw); err != nil {
			return err
		}
		g.Permission, g.Limit = raw.Permission, int64(raw.Limit)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	g.Permission, g.Limit = s, 0
	if i := strings.LastIndex(s, ":"); i > 0 {
		if v, err := strconv.ParseInt(s[i+1:], 10, 64); err == nil {
			g.Permission, g.Limit = s[:i], v
		}
	}
	return nil
}

type CharterInt64 int64

func (n *CharterInt64) UnmarshalJSON(b []byte) error {
	t := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if t == "" || t == "null" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseInt(t, 10, 64)
	if err != nil {
		return err
	}
	*n = CharterInt64(v)
	return nil
}

// VillageCharterGrant is one permission with its ceiling.
type VillageCharterGrant struct {
	Permission string `json:"permission"`
	Limit      int64  `json:"limit,omitempty"`
}

// CharterView handles settlement.charter.view: the offices, who holds them, what
// the viewer may do, the permission catalogue for the editor and the newest audit.
func (h *VillageHandler) CharterView(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	lang := meta.Language
	var view village.CharterView
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
		view, err = h.charterViewOf(ctx, tx, s, p.ID, lang)
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.VillageCharter(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) charterViewOf(ctx context.Context, tx application.Tx, s application.FoundedSettlement, viewerID, lang string,
) (village.CharterView, error) {
	st, err := h.charterTick(ctx, tx, s, lang)
	if err != nil {
		return village.CharterView{}, err
	}
	l := h.limits()
	held := st.heldBy(viewerID)
	v := village.CharterView{
		Village: s.Name, SettlementID: s.CityID,
		Limits: village.CharterLimitsView{MaxOffices: l.MaxOffices, MaxSeats: l.MaxSeats, MaxPermissions: l.MaxPermissions,
			TitleMin: l.TitleMin, TitleMax: l.TitleMax},
	}
	_, v.CanCreate = held.Has(charter.OfficeCreate)
	_, v.CanEdit = held.Has(charter.OfficeEdit)
	_, v.CanAppoint = held.Has(charter.OfficeAppoint)
	_, v.CanDismiss = held.Has(charter.OfficeDismiss)
	v.ZoneMinutes = int(s.Zone() / time.Minute)
	_, v.CanSetZone = held.Has(charter.SettingsTimezone)
	if next := s.TZSetAt.Add(h.tzCooldown); s.TZSetAt.Year() > 2000 && next.After(h.now()) {
		v.ZoneNextChange = &next
	}
	for _, g := range sortedHeld(held) {
		if !permissionShown(g.Permission) {
			continue
		}
		v.Mine = append(v.Mine, village.CharterGrantView{Permission: string(g.Permission), Limit: g.Limit})
	}
	for _, d := range charter.Catalogue() {
		if !d.Active {
			continue // a permission no act asks for is not shown (progressive disclosure); it stays in code (docs/adr/0064)
		}
		v.Permissions = append(v.Permissions, village.CharterPermissionView{Code: string(d.Code), Group: d.Group, Limited: d.Limited, Active: d.Active})
	}
	ids := map[string]bool{}
	for _, seats := range st.seats {
		for _, seat := range seats {
			ids[seat.HolderID] = true
		}
	}
	if st.headHeld != "" {
		ids[st.headHeld] = true
	}
	audit, err := tx.Charters().AuditList(ctx, s.CityID, 20)
	if err != nil {
		return v, err
	}
	for _, a := range audit {
		if a.ActorID != "" {
			ids[a.ActorID] = true
		}
	}
	people, err := h.peopleOf(ctx, tx, ids)
	if err != nil {
		return v, err
	}
	titles := map[string]string{}
	for _, o := range st.offices {
		titles[o.ID] = o.Title
		oid := o.ID
		if oid == "" {
			oid = FounderOfficeID
		}
		ov := village.CharterOfficeView{
			ID: oid, Title: o.Title, Seats: o.Seats, Acquisition: string(o.Acquisition), TermDays: o.TermDays,
			Founder: o.Acquisition == charter.AcquireHead,
			Manager: charter.IsManager(o.Grants),
		}
		for _, g := range o.Grants {
			if !permissionShown(g.Permission) {
				continue
			}
			ov.Grants = append(ov.Grants, village.CharterGrantView{Permission: string(g.Permission), Limit: g.Limit})
		}
		if o.Acquisition == charter.AcquireHead {
			if st.headHeld != "" {
				ov.Holders = append(ov.Holders, people[st.headHeld])
				ov.Mine = st.headHeld == viewerID
			}
		} else {
			for _, seat := range st.seats[o.ID] {
				ov.Holders = append(ov.Holders, people[seat.HolderID])
				ov.Mine = ov.Mine || seat.HolderID == viewerID
			}
		}
		ov.Open = max(o.Seats-len(ov.Holders), 0)
		v.Offices = append(v.Offices, ov)
	}
	for _, a := range audit {
		v.Audit = append(v.Audit, village.CharterAuditView{Action: a.Action, Actor: people[a.ActorID].Name, Office: titles[a.OfficeID],
			Title: stringOf(a.Detail["title"]), At: a.At})
	}
	if err := h.charterPhase2View(ctx, tx, s, st, viewerID, held, &v); err != nil {
		return v, err
	}
	return v, nil
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

func sortedHeld(h charter.Held) []charter.Grant {
	out := make([]charter.Grant, 0, len(h))
	for p, l := range h {
		out = append(out, charter.Grant{Permission: p, Limit: l})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Permission < out[j].Permission })
	return out
}

// peopleOf names players by display name and public code (never an id).
func (h *VillageHandler) peopleOf(ctx context.Context, tx application.Tx, ids map[string]bool) (map[string]village.CharterPersonView, error) {
	out := map[string]village.CharterPersonView{}
	for id := range ids {
		if id == "" {
			continue
		}
		p, err := tx.Players().GetByID(ctx, id)
		if err != nil {
			if stderrors.Is(err, application.ErrPlayerNotFound) {
				continue
			}
			return nil, err
		}
		out[id] = village.CharterPersonView{Name: p.DisplayName, Code: p.PublicCode}
	}
	return out, nil
}

// materialise writes the default charter down (the founder's office) the first
// time an act needs rows. It returns the state afterwards. Callers hold the lock.
func (h *VillageHandler) materialise(ctx context.Context, tx application.Tx, s application.FoundedSettlement, st charterState,
	actorID, lang string,
) (charterState, error) {
	if st.stored {
		return st, nil
	}
	now := h.now()
	o := st.offices[0]
	o.ID = h.ids.NewID()
	if err := tx.Charters().SaveOffice(ctx, s.CityID, o, actorID, now); err != nil {
		return st, err
	}
	if err := tx.Charters().Audit(ctx, application.CharterAuditRow{ID: h.ids.NewID(), SettlementID: s.CityID, ActorID: actorID,
		Action: "charter_written", OfficeID: o.ID, Detail: map[string]any{"title": o.Title}, At: now}); err != nil {
		return st, err
	}
	st.offices = []charter.Office{o}
	st.stored = true
	return st, nil
}

// refuseCharter maps a rail's error to the refusal a player sees.
func refuseCharter(err error) error {
	kind := ""
	switch {
	case stderrors.Is(err, charter.ErrTitleTaken):
		kind = village.CharterTitleTaken
	case stderrors.Is(err, charter.ErrTitle):
		kind = village.CharterTitle
	case stderrors.Is(err, charter.ErrCaps):
		kind = village.CharterCaps
	case stderrors.Is(err, charter.ErrUnknownGrant), stderrors.Is(err, charter.ErrLimitless):
		kind = village.CharterGrant
	case stderrors.Is(err, charter.ErrNotHeld):
		kind = village.CharterNotHeld
	case stderrors.Is(err, charter.ErrLastManager):
		kind = village.CharterLastManager
	case stderrors.Is(err, charter.ErrHeadOffice):
		kind = village.CharterHeldOffice
	case stderrors.Is(err, charter.ErrAcquisition):
		kind = village.CharterAcquisition
	case stderrors.Is(err, charter.ErrSeatsFull):
		kind = village.CharterSeatsFull
	case stderrors.Is(err, charter.ErrAlreadySeated):
		kind = village.CharterAlreadySeated
	case stderrors.Is(err, errZoneInvalid):
		kind = village.CharterZoneInvalid
	case stderrors.Is(err, errZoneCooldown):
		kind = village.CharterZoneCooldown
	}
	if kind == "" {
		return err
	}
	return refuseVillage(kind, village.AddrVillageCharter)
}

func grantsOf(in []VillageCharterGrant) []charter.Grant {
	out := make([]charter.Grant, 0, len(in))
	for _, g := range in {
		out = append(out, charter.Grant{Permission: charter.Permission(strings.TrimSpace(g.Permission)), Limit: g.Limit})
	}
	return out
}

// widened is the grants of `next` that `prev` did not hold as widely: only those
// need the actor's cover (rail R1), so a deputy may edit the title of an office
// whose powers they do not themselves hold.
func widened(prev, next []charter.Grant) []charter.Grant {
	old := charter.HeldBy(prev)
	var out []charter.Grant
	for _, g := range next {
		if have, ok := old.Has(g.Permission); !ok || !charter.Covers(have, g.Limit) {
			out = append(out, g)
		}
	}
	return out
}

// CharterOfficeSave handles settlement.charter.office.save: create an office (the
// permission office.create) or change one (office.edit).
func (h *VillageHandler) CharterOfficeSave(ctx context.Context, meta envelope.Metadata, req VillageCharterRequest) (*presentation.Response, error) {
	lang := meta.Language
	var done *village.CharterChangedView
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
		if err := tx.Charters().Lock(ctx, s.CityID); err != nil {
			return err
		}
		st, err := h.charterTick(ctx, tx, s, lang)
		if err != nil {
			return err
		}
		held := st.heldBy(p.ID)
		req.Office = resolveOffice(st, req.Office)
		creating := strings.TrimSpace(req.Office) == ""
		need := charter.OfficeEdit
		if creating {
			need = charter.OfficeCreate
		}
		if _, ok := held.Has(need); !ok {
			return application.ErrNotOfficeHolder.WithDetail("office", officeFor(s.Tier)).WithDetail("permission", string(need))
		}
		if fresh, err := h.reserve(ctx, tx, p.ID, meta); err != nil {
			return err
		} else if !fresh {
			return nil
		}
		if st, err = h.materialise(ctx, tx, s, st, p.ID, lang); err != nil {
			return err
		}
		req.Office = resolveOffice(st, req.Office)
		lim := h.limits()
		grants, err := charter.NormaliseGrants(grantsOf(req.Grants))
		if err != nil {
			return refuseCharter(err)
		}
		var prev *charter.Office
		idx := -1
		for i := range st.offices {
			if st.offices[i].ID == strings.TrimSpace(req.Office) {
				prev, idx = &st.offices[i], i
			}
		}
		if !creating && prev == nil {
			return refuseVillage(village.VillageNotFound, village.AddrVillageCharter)
		}
		var taken []string
		for _, o := range st.offices {
			if prev == nil || o.ID != prev.ID {
				taken = append(taken, o.Title)
			}
		}
		title, err := charter.ValidateTitle(req.Title, taken, lim)
		if err != nil {
			return refuseCharter(err)
		}
		next := charter.Office{ID: h.ids.NewID(), Title: title, Seats: int(req.Seats), Grants: grants,
			Acquisition: charter.Acquisition(req.Acquisition), TermDays: int(req.TermDays)}
		if next.Seats == 0 {
			next.Seats = 1
		}
		if next.Acquisition == "" {
			next.Acquisition = charter.AcquireAppointment
		}
		var before []charter.Grant
		if prev != nil {
			next.ID, before = prev.ID, prev.Grants
			if prev.Acquisition == charter.AcquireHead {
				// the founder's office: only its title (and term) are the players' to change
				next.Seats, next.Grants, next.Acquisition = prev.Seats, prev.Grants, prev.Acquisition
				grants = prev.Grants
			}
			if next.Seats < len(st.seats[prev.ID]) && prev.Acquisition != charter.AcquireHead {
				return refuseCharter(charter.ErrSeatsFull)
			}
		}
		if next.Acquisition == charter.AcquireHead && (prev == nil || prev.Acquisition != charter.AcquireHead) {
			return refuseCharter(charter.ErrAcquisition) // only the founder's office is filled by the head seat
		}
		if next.Acquisition == charter.AcquireElection && next.TermDays == 0 {
			next.TermDays = h.cset().ElectionTermDays
		}
		if prev != nil {
			next.Deputy = prev.Deputy
		}
		if req.Deputy != nil {
			next.Deputy = *req.Deputy
		}
		if next.Deputy {
			if next.Acquisition == charter.AcquireHead {
				return refuseCharter(charter.ErrAcquisition)
			}
			for _, o := range st.offices {
				if o.Deputy && !o.Closed && (prev == nil || o.ID != prev.ID) {
					return refuseVillage(village.CharterDeputyTaken, village.AddrVillageCharter)
				}
			}
		}
		open := len(st.offices)
		if prev == nil {
			open++
		}
		if err := charter.ValidateOffice(next, open, lim); err != nil {
			return refuseCharter(err)
		}
		if err := held.CanGrant(widened(before, grants)); err != nil { // R1
			return refuseCharter(err)
		}
		after := make([]charter.OfficeState, 0, len(st.offices)+1)
		for i, o := range st.offices {
			if i == idx {
				o = next
			}
			after = append(after, charter.OfficeState{Office: o, Held: st.held(o)})
		}
		if prev == nil {
			after = append(after, charter.OfficeState{Office: next, Held: false})
		}
		if err := charter.ManagerGuard(after); err != nil { // R2
			return refuseCharter(err)
		}
		now := h.now()
		// A structural change (a key permission, how the office is filled, the deputy) goes
		// to the residents once there are enough of them to ask.
		if prev != nil && charter.Structural(*prev, next) {
			residents, err := tx.Settlements().ResidentCount(ctx, s.CityID)
			if err != nil {
				return err
			}
			if h.cset().NeedsVote(int(residents)) {
				if _, ok := held.Has(charter.CharterAmend); !ok {
					return application.ErrNotOfficeHolder.WithDetail("office", officeFor(s.Tier)).WithDetail("permission", string(charter.CharterAmend))
				}
				id, err := h.openAmendment(ctx, tx, s, "save", next, p.ID, now)
				if err != nil {
					return err
				}
				done = &village.CharterChangedView{Action: "amendment_proposed", Title: next.Title, BallotID: id}
				return nil
			}
		}
		if err := tx.Charters().SaveOffice(ctx, s.CityID, next, p.ID, now); err != nil {
			return err
		}
		action := "office_changed"
		if prev == nil {
			action = "office_created"
		}
		if err := tx.Charters().Audit(ctx, application.CharterAuditRow{ID: h.ids.NewID(), SettlementID: s.CityID, ActorID: p.ID,
			Action: action, OfficeID: next.ID, At: now, Detail: map[string]any{
				"title": next.Title, "seats": next.Seats, "grants": grantStrings(grants), "before": grantStrings(before)}}); err != nil {
			return err
		}
		done = &village.CharterChangedView{Action: action, Title: next.Title}
		return appendVillageEvent(ctx, tx, meta, "charter_changed", s.CityID, map[string]any{
			"settlement_id": s.CityID, "action": action, "office_id": next.ID, "by": p.ID})
	})
	return h.charterAnswer(ctx, meta, lang, err, done)
}

func grantStrings(gs []charter.Grant) []string {
	out := make([]string, 0, len(gs))
	for _, g := range gs {
		if g.Limit > 0 {
			out = append(out, string(g.Permission)+"<="+itoa(g.Limit))
		} else {
			out = append(out, string(g.Permission))
		}
	}
	return out
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// charterAnswer finishes a charter act: a refusal, a replay (the same answer
// again) or the changed screen.
func (h *VillageHandler) charterAnswer(ctx context.Context, meta envelope.Metadata, lang string, err error, done *village.CharterChangedView) (*presentation.Response, error) {
	if resp, ferr := h.villageFinish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	if done == nil { // a redelivered request: show the charter as it stands
		return h.CharterView(ctx, meta)
	}
	return village.VillageCharterChanged(h.screen(meta, lang), *done), nil
}

// CharterOfficeClose handles settlement.charter.office.close (office.edit): the
// office ends, its seats end with it. The founder's office cannot be closed.
func (h *VillageHandler) CharterOfficeClose(ctx context.Context, meta envelope.Metadata, req VillageCharterRequest) (*presentation.Response, error) {
	lang := meta.Language
	var done *village.CharterChangedView
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
		if err := tx.Charters().Lock(ctx, s.CityID); err != nil {
			return err
		}
		st, err := h.charterTick(ctx, tx, s, lang)
		if err != nil {
			return err
		}
		if _, ok := st.heldBy(p.ID).Has(charter.OfficeEdit); !ok {
			return application.ErrNotOfficeHolder.WithDetail("office", officeFor(s.Tier)).WithDetail("permission", string(charter.OfficeEdit))
		}
		if fresh, err := h.reserve(ctx, tx, p.ID, meta); err != nil {
			return err
		} else if !fresh {
			return nil
		}
		var target *charter.Office
		for i := range st.offices {
			if st.offices[i].ID != "" && st.offices[i].ID == strings.TrimSpace(req.Office) {
				target = &st.offices[i]
			}
		}
		if target == nil {
			return refuseVillage(village.VillageNotFound, village.AddrVillageCharter)
		}
		if target.Acquisition == charter.AcquireHead {
			return refuseCharter(charter.ErrHeadOffice)
		}
		after := make([]charter.OfficeState, 0, len(st.offices))
		for _, o := range st.offices {
			if o.ID == target.ID {
				o.Closed = true
			}
			after = append(after, charter.OfficeState{Office: o, Held: st.held(o)})
		}
		if err := charter.ManagerGuard(after); err != nil {
			return refuseCharter(err)
		}
		now := h.now()
		if charter.ClosingNeedsVote(*target) {
			residents, err := tx.Settlements().ResidentCount(ctx, s.CityID)
			if err != nil {
				return err
			}
			if h.cset().NeedsVote(int(residents)) {
				if _, ok := st.heldBy(p.ID).Has(charter.CharterAmend); !ok {
					return application.ErrNotOfficeHolder.WithDetail("office", officeFor(s.Tier)).WithDetail("permission", string(charter.CharterAmend))
				}
				id, err := h.openAmendment(ctx, tx, s, "close", *target, p.ID, now)
				if err != nil {
					return err
				}
				done = &village.CharterChangedView{Action: "amendment_proposed", Title: target.Title, BallotID: id}
				return nil
			}
		}
		if err := tx.Charters().EndSeatsOf(ctx, target.ID, "office_closed", now); err != nil {
			return err
		}
		if err := tx.Charters().CloseOffice(ctx, target.ID, now); err != nil {
			return err
		}
		if err := tx.Charters().Audit(ctx, application.CharterAuditRow{ID: h.ids.NewID(), SettlementID: s.CityID, ActorID: p.ID,
			Action: "office_closed", OfficeID: target.ID, At: now, Detail: map[string]any{"title": target.Title}}); err != nil {
			return err
		}
		done = &village.CharterChangedView{Action: "office_closed", Title: target.Title}
		return appendVillageEvent(ctx, tx, meta, "charter_changed", s.CityID, map[string]any{
			"settlement_id": s.CityID, "action": "office_closed", "office_id": target.ID, "by": p.ID})
	})
	return h.charterAnswer(ctx, meta, lang, err, done)
}

// CharterAppoint handles settlement.charter.appoint (office.appoint): a resident,
// named by public code, takes a seat. The actor must hold everything the office
// holds (rail R1): an office cannot hand out powers its appointer lacks.
func (h *VillageHandler) CharterAppoint(ctx context.Context, meta envelope.Metadata, req VillageCharterRequest) (*presentation.Response, error) {
	return h.charterSeat(ctx, meta, req, true)
}

// CharterDismiss handles settlement.charter.dismiss (office.dismiss).
func (h *VillageHandler) CharterDismiss(ctx context.Context, meta envelope.Metadata, req VillageCharterRequest) (*presentation.Response, error) {
	return h.charterSeat(ctx, meta, req, false)
}

// CharterResign handles settlement.charter.resign: leave an office of one's own.
func (h *VillageHandler) CharterResign(ctx context.Context, meta envelope.Metadata, req VillageCharterRequest) (*presentation.Response, error) {
	req.Player = ""
	return h.charterSeat(ctx, meta, req, false)
}

func (h *VillageHandler) charterSeat(ctx context.Context, meta envelope.Metadata, req VillageCharterRequest, appoint bool) (*presentation.Response, error) {
	lang := meta.Language
	var done *village.CharterChangedView
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
		if err := tx.Charters().Lock(ctx, s.CityID); err != nil {
			return err
		}
		st, err := h.charterTick(ctx, tx, s, lang)
		if err != nil {
			return err
		}
		held := st.heldBy(p.ID)
		resign := !appoint && strings.TrimSpace(req.Player) == ""
		need := charter.OfficeAppoint
		if !appoint && !resign {
			need = charter.OfficeDismiss
		}
		if !resign {
			if _, ok := held.Has(need); !ok {
				return application.ErrNotOfficeHolder.WithDetail("office", officeFor(s.Tier)).WithDetail("permission", string(need))
			}
		}
		if fresh, err := h.reserve(ctx, tx, p.ID, meta); err != nil {
			return err
		} else if !fresh {
			return nil
		}
		var target *charter.Office
		for i := range st.offices {
			if st.offices[i].ID != "" && st.offices[i].ID == strings.TrimSpace(req.Office) {
				target = &st.offices[i]
			}
		}
		if target == nil {
			return refuseVillage(village.VillageNotFound, village.AddrVillageCharter)
		}
		if target.Acquisition != charter.AcquireAppointment && !resign {
			return refuseCharter(charter.ErrAcquisition) // the founder's office follows the governance seat
		}
		whom := p.ID
		if !resign {
			who, err := tx.Charters().ResidentByCode(ctx, s.CityID, strings.TrimSpace(req.Player))
			if err != nil {
				return err
			}
			if who == nil {
				return refuseVillage(village.VillageNotResident, village.AddrVillageCharter)
			}
			whom = who.ID
		}
		now := h.now()
		action := "seat_ended"
		switch {
		case appoint:
			if err := held.CanGrant(target.Grants); err != nil { // R1
				return refuseCharter(err)
			}
			if len(st.seats[target.ID]) >= target.Seats {
				return refuseCharter(charter.ErrSeatsFull)
			}
			if recalled, err := tx.Charters().RecalledSince(ctx, target.ID, whom, now.Add(-time.Duration(h.cset().RecallCooldownDays)*24*time.Hour)); err != nil {
				return err
			} else if recalled {
				return refuseVillage(village.CharterRecalledRecent, village.AddrVillageCharter)
			}
			ok, err := tx.Charters().Seat(ctx, application.CharterSeat{ID: h.ids.NewID(), OfficeID: target.ID, HolderID: whom, AppointedBy: p.ID, Since: now})
			if err != nil {
				return err
			}
			if !ok {
				return refuseCharter(charter.ErrAlreadySeated)
			}
			action = "seat_filled"
		default:
			if target.Acquisition == charter.AcquireHead {
				return refuseCharter(charter.ErrHeadOffice)
			}
			reason := "dismissed"
			if resign {
				reason = "resigned"
			}
			// R2 over the state after the seat ends
			after := make([]charter.OfficeState, 0, len(st.offices))
			for _, o := range st.offices {
				heldAfter := st.held(o)
				if o.ID == target.ID {
					n := 0
					for _, seat := range st.seats[o.ID] {
						if seat.HolderID != whom {
							n++
						}
					}
					heldAfter = n > 0
				}
				after = append(after, charter.OfficeState{Office: o, Held: heldAfter})
			}
			if err := charter.ManagerGuard(after); err != nil {
				return refuseCharter(err)
			}
			ok, err := tx.Charters().EndSeat(ctx, target.ID, whom, reason, now)
			if err != nil {
				return err
			}
			if !ok {
				return refuseVillage(village.VillageNotFound, village.AddrVillageCharter)
			}
			action = "seat_" + reason
		}
		if err := tx.Charters().Audit(ctx, application.CharterAuditRow{ID: h.ids.NewID(), SettlementID: s.CityID, ActorID: p.ID,
			Action: action, OfficeID: target.ID, At: now, Detail: map[string]any{"title": target.Title, "holder": whom}}); err != nil {
			return err
		}
		done = &village.CharterChangedView{Action: action, Title: target.Title}
		return appendVillageEvent(ctx, tx, meta, "charter_changed", s.CityID, map[string]any{
			"settlement_id": s.CityID, "action": action, "office_id": target.ID, "holder_id": whom, "by": p.ID})
	})
	return h.charterAnswer(ctx, meta, lang, err, done)
}

// resolveOffice maps the founder's stable id to the head office's real id (when the
// charter is written) or leaves it as "founder" (when it is not yet, so the caller
// can still tell an edit from a creation).
func resolveOffice(st charterState, id string) string {
	id = strings.TrimSpace(id)
	if id != FounderOfficeID {
		return id
	}
	for _, o := range st.offices {
		if o.Acquisition == charter.AcquireHead && o.ID != "" {
			return o.ID
		}
	}
	return id
}

var (
	errZoneInvalid  = stderrors.New("charter: not a usable time zone")
	errZoneCooldown = stderrors.New("charter: the time zone was changed too recently")
)

// VillageZoneRequest is the payload of settlement.timezone.set.
type VillageZoneRequest struct {
	// OffsetMinutes is the zone as minutes east of UTC, a multiple of 15, from -720
	// to 840 (a number or a string).
	OffsetMinutes CharterInt `json:"offset_minutes"`
}

// TimezoneSet handles settlement.timezone.set (the permission settings.timezone): the
// settlement's own time zone, which its daily rhythms follow. It may be changed once
// per settlement.timezone_cooldown; every change is in the charter log.
func (h *VillageHandler) TimezoneSet(ctx context.Context, meta envelope.Metadata, req VillageZoneRequest) (*presentation.Response, error) {
	lang := meta.Language
	var done *village.CharterChangedView
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
		if err := tx.Charters().Lock(ctx, s.CityID); err != nil {
			return err
		}
		if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.SettingsTimezone); err != nil {
			return err
		}
		if fresh, err := h.reserve(ctx, tx, p.ID, meta); err != nil {
			return err
		} else if !fresh {
			return nil
		}
		// re-read under the lock: two replicas must not both pass the cooldown
		s, err = tx.Settlements().ByID(ctx, s.CityID)
		if err != nil {
			return err
		}
		off := time.Duration(req.OffsetMinutes) * time.Minute
		if !gametime.ValidOffset(off) || req.OffsetMinutes%15 != 0 {
			return refuseCharter(errZoneInvalid)
		}
		now := h.now()
		if s.TZSetAt.Year() > 2000 && now.Before(s.TZSetAt.Add(h.tzCooldown)) {
			return refuseCharter(errZoneCooldown)
		}
		minutes := int(req.OffsetMinutes)
		if err := tx.Settlements().SetTimezone(ctx, s.CityID, &minutes, now); err != nil {
			return err
		}
		if err := tx.Charters().Audit(ctx, application.CharterAuditRow{ID: h.ids.NewID(), SettlementID: s.CityID, ActorID: p.ID,
			Action: "timezone_changed", At: now, Detail: map[string]any{"from_minutes": int(s.Zone() / time.Minute), "to_minutes": minutes}}); err != nil {
			return err
		}
		done = &village.CharterChangedView{Action: "timezone_changed", Title: ""}
		return appendVillageEvent(ctx, tx, meta, "charter_changed", s.CityID, map[string]any{
			"settlement_id": s.CityID, "action": "timezone_changed", "by": p.ID, "zone_minutes": minutes})
	})
	return h.charterAnswer(ctx, meta, lang, err, done)
}

// openAmendment puts a structural change to the residents: the ballot carries the
// change, which is checked against the rails again when it carries.
func (h *VillageHandler) openAmendment(ctx context.Context, tx application.Tx, s application.FoundedSettlement, op string, o charter.Office, by string, now time.Time) (string, error) {
	set := h.cset()
	eligible, err := tx.Charters().Eligible(ctx, s.CityID, h.voterCut(now))
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(proposalJSON{Op: op, Office: officeToJSON(o), By: by})
	if err != nil {
		return "", err
	}
	id := h.ids.NewID()
	closes := now.Add(time.Duration(set.AmendVoteHours) * time.Hour)
	actionID, err := h.schedule(ctx, tx, application.CharterBallotActionType, application.CharterBallotReference, id, s.CityID, now, closes)
	if err != nil {
		return "", err
	}
	ok, err := tx.Charters().OpenBallot(ctx, application.CharterBallot{ID: id, SettlementID: s.CityID, Kind: string(charter.BallotAmendment),
		OfficeID: o.ID, OpenedBy: by, Proposal: raw, OpensAt: now, ClosesAt: closes, Eligible: eligible, ActionID: actionID})
	if err != nil {
		return "", err
	}
	if !ok {
		return "", refuseVillage(village.CharterVotePending, village.AddrVillageCharter)
	}
	if err := tx.Charters().Audit(ctx, application.CharterAuditRow{ID: h.ids.NewID(), SettlementID: s.CityID, ActorID: by,
		Action: "amendment_proposed", OfficeID: o.ID, At: now, Detail: map[string]any{"ballot": id, "op": op, "title": o.Title, "eligible": eligible}}); err != nil {
		return "", err
	}
	return id, nil
}

// permissionShown reports whether a permission is shown to players: only those some act asks for today. The rest stay in the
// catalogue and in the offices that hold them; they appear when their act exists.
func permissionShown(p charter.Permission) bool {
	d, ok := charter.Lookup(p)
	return ok && d.Active
}
