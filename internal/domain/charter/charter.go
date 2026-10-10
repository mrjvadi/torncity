// Package charter is the players' constitution of a settlement (docs/adr/0044
// section 6): a list of offices the players create, what each may do (atomic
// permissions defined here, in code), how each is filled, and the safety rails
// no charter can change. It is pure: no storage, no clock beyond what callers
// pass.
//
// Real basis (recorded 2026-10-05): optional municipal charter laws let a town
// "establish, alter and abolish offices, positions and employments and define
// their functions, powers, duties, terms and compensation" (New Jersey Optional
// Municipal Charter Law, N.J.S.A. 40:69A); a mayor may delegate or withdraw
// functions among their office EXCEPT the power to appoint or remove officials
// (New York City Charter ch. 1); appointed officers may be removed for cause by
// the appointing officer (Tennessee MTAS on department heads; Ohio Revised Code
// ch. 733). Those become the rails: nobody grants what they do not hold (R1),
// an office manager always exists (R2), every change is audited (R3), sizes are
// capped (R4).
package charter

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Permission is an atomic thing an office may do. The catalogue below is the only
// source: a new permission is a code change, because the rules live in code.
type Permission string

// The catalogue (ADR 0044 section 6.3). Active ones are enforced by an act today;
// the others can be granted but nothing asks for them yet (the system they gate is
// not built), so a charter written now keeps working when it is.
const (
	TreasurySpend Permission = "treasury.spend" // limit: most per act, minor units (0: no limit)
	PayrollSet    Permission = "payroll.set"
	BudgetAlloc   Permission = "budget.allocate"
	StorageTake   Permission = "storage.take" // take goods out of the settlement's stock

	FiscalSalesTax  Permission = "fiscal.set:sales_tax"
	FiscalShopPrice Permission = "fiscal.set:shop_price"
	FiscalMarketFee Permission = "fiscal.set:market_fee"
	FiscalLevy      Permission = "fiscal.set:levy"

	RoadDraw Permission = "road.draw"
	// LandClear orders the clearing of trees and rocks off the commons and the treasury's lots (docs/adr/0065).
	LandClear      Permission = "land.clear"
	ZoneOpen       Permission = "zone.open"
	ZoneClose      Permission = "zone.close"
	LotSell        Permission = "lot.sell"
	PublicBuild    Permission = "public.build"
	PublicDemolish Permission = "public.demolish"

	CitizenAdmit Permission = "citizen.admit"
	CitizenBan   Permission = "citizen.ban"
	StaffHire    Permission = "staff.hire"
	StaffFire    Permission = "staff.fire"
	JobsPost     Permission = "jobs.post"

	ResearchStart Permission = "research.start"
	// ResearchShare proposes, accepts and ends the settlement's research-sharing pacts (ADR 0048).
	ResearchShare Permission = "research.share"
	// TradeExport puts the settlement's goods on sale to the travelling trader and sets what it keeps back (ADR 0049).
	TradeExport Permission = "trade.export"

	PolicePatrol Permission = "police.patrol"
	PoliceFine   Permission = "police.fine"
	CourtJudge   Permission = "court.judge"

	ElectionCall  Permission = "election.call"
	OfficeCreate  Permission = "office.create"
	OfficeEdit    Permission = "office.edit"
	OfficeAppoint Permission = "office.appoint"
	OfficeDismiss Permission = "office.dismiss"
	CharterAmend  Permission = "charter.amend"

	TreatyPropose Permission = "treaty.propose"
	UnionPropose  Permission = "union.propose"
	RaidDeclare   Permission = "raid.declare"

	NoticePost Permission = "notice.post"

	// SettingsTimezone changes the settlement's own time zone, which its daily
	// rhythms (the shop's morning, the stores' day, the market day) follow.
	SettingsTimezone Permission = "settings.timezone"

	// CurrencyCharter charters the settlement's own money by hand (the fee and the first deposit
	// from the treasury); the founding does it itself when the grant covers it (docs/adr/0033 6.2).
	CurrencyCharter Permission = "currency.charter"
	// CurrencyIssue makes more of the settlement's money against a deposit, burns what the treasury holds and
	// retires the money (docs/adr/0033 6.4, 6.7, 6.13); BankPolicy runs the money's reserve: the intervention on
	// the book and the withdrawal of the excess (ADR 0033 6.7). Both are delegable like any permission.
	CurrencyIssue Permission = "currency.issue"
	BankPolicy    Permission = "bank.policy"
)

// Def describes one permission.
type Def struct {
	Code  Permission
	Group string
	// Limited says the permission takes a limit (a ceiling on one act).
	Limited bool
	// Active says an act in the game asks for it today.
	Active bool
}

var catalogue = []Def{
	{TreasurySpend, "treasury", true, true}, {PayrollSet, "treasury", true, false}, {BudgetAlloc, "treasury", false, false},
	{StorageTake, "treasury", false, true},
	{FiscalSalesTax, "fiscal", false, true}, {FiscalShopPrice, "fiscal", false, true},
	{FiscalMarketFee, "fiscal", false, false}, {FiscalLevy, "fiscal", false, false},
	{RoadDraw, "land", false, true}, {LandClear, "land", false, true}, {ZoneOpen, "land", false, false}, {ZoneClose, "land", false, false},
	{LotSell, "land", false, true}, {PublicBuild, "land", false, true}, {PublicDemolish, "land", false, true},
	{CitizenAdmit, "people", false, false}, {CitizenBan, "people", false, false},
	{StaffHire, "people", false, true}, {StaffFire, "people", false, true}, {JobsPost, "people", false, true},
	{ResearchStart, "knowledge", false, true}, {ResearchShare, "knowledge", false, true}, {TradeExport, "treasury", false, true},
	{PolicePatrol, "order", false, false}, {PoliceFine, "order", true, false}, {CourtJudge, "order", false, false},
	{ElectionCall, "politics", false, false},
	{OfficeCreate, "politics", false, true}, {OfficeEdit, "politics", false, true},
	{OfficeAppoint, "politics", false, true}, {OfficeDismiss, "politics", false, true},
	{CharterAmend, "politics", false, true},
	{TreatyPropose, "foreign", false, false}, {UnionPropose, "foreign", false, false}, {RaidDeclare, "foreign", false, false},
	{NoticePost, "info", false, false},
	{SettingsTimezone, "settings", false, true},
	{CurrencyCharter, "currency", false, true},
	{CurrencyIssue, "currency", false, true}, {BankPolicy, "currency", false, true},
}

var byCode = func() map[Permission]Def {
	m := make(map[Permission]Def, len(catalogue))
	for _, d := range catalogue {
		m[d.Code] = d
	}
	return m
}()

// Catalogue lists every permission, in a stable order.
func Catalogue() []Def { return append([]Def(nil), catalogue...) }

// Lookup returns a permission's definition.
func Lookup(p Permission) (Def, bool) { d, ok := byCode[p]; return d, ok }

// AllPermissions is every permission with no limit: what the founder's office holds.
func AllPermissions() []Grant {
	out := make([]Grant, 0, len(catalogue))
	for _, d := range catalogue {
		out = append(out, Grant{Permission: d.Code})
	}
	return out
}

// Grant is one permission with its limit; a limit of 0 means no ceiling.
type Grant struct {
	Permission Permission `json:"p"`
	Limit      int64      `json:"l,omitempty"`
}

// Acquisition is how an office is filled.
type Acquisition string

const (
	// AcquireHead is the founder's office: its holder is whoever holds the
	// settlement's head office in the governance seats (so the existing
	// succession and elections keep working). One per charter.
	AcquireHead Acquisition = "head"
	// AcquireAppointment: an office holding office.appoint seats and unseats it.
	AcquireAppointment Acquisition = "appointment"
	// AcquireElection: the residents elect it. Declared in the schema; the
	// election and recall machinery is phase 2 (docs/research backend audit).
	AcquireElection Acquisition = "election"
)

// Office is one office of a charter.
type Office struct {
	ID          string
	Title       string
	Seats       int
	Grants      []Grant
	Acquisition Acquisition
	// AppointerID is the office whose holders appoint to this one ("" : any
	// holder of office.appoint).
	AppointerID string
	TermDays    int
	// Deputy marks the office that holds the founder's powers (less the ones an
	// acting head may not use) while the head seat is vacant. At most one.
	Deputy bool
	Closed bool
}

// Limits are the caps of rail R4 (config charter.*).
type Limits struct {
	MaxOffices, MaxSeats, MaxPermissions int
	TitleMin, TitleMax                   int
	// Reserved are extra words no title may be (content: founding.yml reserved_names).
	Reserved []string
}

// Errors the rails return; handlers map them to refusal codes.
var (
	ErrTitle         = errors.New("charter: the title is not allowed")
	ErrTitleTaken    = errors.New("charter: another office has this title")
	ErrCaps          = errors.New("charter: a size cap is exceeded")
	ErrUnknownGrant  = errors.New("charter: unknown permission")
	ErrLimitless     = errors.New("charter: this permission takes no limit")
	ErrNotHeld       = errors.New("charter: nobody may grant a permission they do not hold")
	ErrLastManager   = errors.New("charter: the settlement would be left with no office manager")
	ErrHeadOffice    = errors.New("charter: the founder's office cannot be closed or lose its manager powers")
	ErrAcquisition   = errors.New("charter: this way of filling an office is not available")
	ErrSeatsFull     = errors.New("charter: the office has no free seat")
	ErrAlreadySeated = errors.New("charter: the player already holds this office")
	ErrOfficeClosed  = errors.New("charter: the office is closed")
)

// reserved words nobody may use as a title whatever the content says (operator names).
// The content adds its own: the founding file's reserved names (the neutral city and
// the operator words in Persian).
var reserved = []string{"admin", "operator", "support", "system"}

// ValidateTitle applies the title rules of ADR 0044 6.1: trimmed, between the
// limits in characters, not reserved, unique among the open offices.
func ValidateTitle(title string, taken []string, l Limits) (string, error) {
	t := strings.Join(strings.Fields(title), " ")
	n := utf8.RuneCountInString(t)
	if n < l.TitleMin || n > l.TitleMax {
		return "", ErrTitle
	}
	low := strings.ToLower(t)
	for _, r := range append(append([]string(nil), reserved...), l.Reserved...) {
		if low == strings.ToLower(strings.TrimSpace(r)) {
			return "", ErrTitle
		}
	}
	for _, o := range taken {
		if strings.ToLower(o) == low {
			return "", ErrTitleTaken
		}
	}
	return t, nil
}

// NormaliseGrants checks every grant against the catalogue, drops duplicates (the
// larger limit wins; 0 is the largest) and returns them sorted.
func NormaliseGrants(in []Grant) ([]Grant, error) {
	best := map[Permission]Grant{}
	for _, g := range in {
		d, ok := byCode[g.Permission]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownGrant, g.Permission)
		}
		if g.Limit < 0 || (g.Limit > 0 && !d.Limited) {
			return nil, fmt.Errorf("%w: %s", ErrLimitless, g.Permission)
		}
		if old, dup := best[g.Permission]; !dup || Covers(g.Limit, old.Limit) {
			best[g.Permission] = g
		}
	}
	out := make([]Grant, 0, len(best))
	for _, g := range best {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Permission < out[j].Permission })
	return out, nil
}

// Covers reports whether a held limit covers a wanted one (0 is no ceiling).
func Covers(have, want int64) bool {
	if have == 0 {
		return true
	}
	return want != 0 && have >= want
}

// Held is what a set of offices together hold: the best limit of each permission.
type Held map[Permission]int64

// HeldBy folds grants of the offices a player sits in.
func HeldBy(offices ...[]Grant) Held {
	h := Held{}
	for _, gs := range offices {
		for _, g := range gs {
			if old, ok := h[g.Permission]; !ok || Covers(g.Limit, old) {
				h[g.Permission] = g.Limit
			}
		}
	}
	return h
}

// Has reports whether the permission is held; limit is its ceiling (0: none).
func (h Held) Has(p Permission) (limit int64, ok bool) { l, ok := h[p]; return l, ok }

// CanGrant is rail R1: every wanted grant must be held at least as widely.
func (h Held) CanGrant(wanted []Grant) error {
	for _, g := range wanted {
		have, ok := h[g.Permission]
		if !ok || !Covers(have, g.Limit) {
			return fmt.Errorf("%w: %s", ErrNotHeld, g.Permission)
		}
	}
	return nil
}

// ValidateOffice applies the caps of rail R4 to an office being saved and the
// number of open offices after the save.
func ValidateOffice(o Office, openAfter int, l Limits) error {
	if o.Seats < 1 || o.Seats > l.MaxSeats || len(o.Grants) > l.MaxPermissions || openAfter > l.MaxOffices {
		return ErrCaps
	}
	switch o.Acquisition {
	case AcquireHead, AcquireAppointment, AcquireElection:
	default:
		return ErrAcquisition
	}
	return nil
}

// IsManager says an office can edit the charter: it holds office.edit and
// charter.amend (rail R2).
func IsManager(gs []Grant) bool {
	var edit, amend bool
	for _, g := range gs {
		edit = edit || g.Permission == OfficeEdit
		amend = amend || g.Permission == CharterAmend
	}
	return edit && amend
}

// OfficeState is an office with whether anyone can act in it: the head office
// always can (its holder is the governance head), others when a seat is held.
type OfficeState struct {
	Office
	Held bool
}

// ManagerGuard is rail R2 over the offices as they would stand after a change:
// at least one open office is a manager and is held.
func ManagerGuard(offices []OfficeState) error {
	for _, o := range offices {
		if !o.Closed && o.Held && IsManager(o.Grants) {
			return nil
		}
	}
	return ErrLastManager
}

// FoundersOffice is the office every charter starts with: the head office, every
// permission, no limit. The founder may rename it; it is never closed.
func FoundersOffice(id, title string) Office {
	return Office{ID: id, Title: title, Seats: 1, Grants: AllPermissions(), Acquisition: AcquireHead}
}
