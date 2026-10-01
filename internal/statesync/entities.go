package statesync

import (
	"encoding/json"
	"time"
)

// The data of each kind. Neutral by contract (docs/adr/0039): codes,
// numbers, ids and RFC 3339 instants; never a sentence. A name that is a
// proper noun (the player's own, a settlement's) is data; a content name
// (an item, a skill, a city) is its code, worded by each edge from the
// content catalogue.

// PlayerData is KindPlayer.
type PlayerData struct {
	Name        string `json:"name"`
	Code        string `json:"code"`
	Lang        string `json:"lang"`
	Status      string `json:"status"`
	Level       int    `json:"level"`
	XP          int64  `json:"xp"`
	NextLevelXP int64  `json:"next_level_xp"`
	// Rank is the life rank's code (catalogue table life_rank).
	Rank string `json:"rank"`
}

// Regen is how a meter refills between writes: Amount every EverySeconds,
// the elapsed time counted at BPS/10000 (a hard-pressed body regenerates
// slower). A client shows min(Max, Value + floor(elapsed*BPS/10000 /
// EverySeconds) * Amount), elapsed measured from AsOf on the server's clock.
type Regen struct {
	Amount       int   `json:"amount"`
	EverySeconds int64 `json:"every_seconds"`
	BPS          int   `json:"bps"`
}

// Meter is one vital as stored: its value at AsOf, and how it refills.
type Meter struct {
	Value int        `json:"value"`
	Max   int        `json:"max"`
	AsOf  *time.Time `json:"as_of"`
	Regen *Regen     `json:"regen,omitempty"`
}

// VitalsData is KindVitals.
type VitalsData struct {
	Energy Meter `json:"energy"`
	Nerve  Meter `json:"nerve"`
	Health Meter `json:"health"`
}

// WalletData is KindWallet, one currency.
type WalletData struct {
	Currency string `json:"currency"`
	Cash     int64  `json:"cash"`
	Bank     int64  `json:"bank"`
	// Premium marks the premium currency (Nil).
	Premium bool `json:"premium"`
}

// PieceData is one unique item piece.
type PieceData struct {
	ID       string `json:"id"`
	Holding  string `json:"holding"`
	Quality  int    `json:"quality"`
	UsesLeft *int   `json:"uses_left"`
}

// InventoryData is KindInventory, one item code.
type InventoryData struct {
	Item string `json:"item"`
	Qty  int64  `json:"qty"`
	// Holdings is how many are held each way (carried, equipped, stored...).
	Holdings map[string]int64 `json:"holdings"`
	Pieces   []PieceData      `json:"pieces"`
}

// SkillData is KindSkill.
type SkillData struct {
	Skill string `json:"skill"`
	Level int    `json:"level"`
	XP    int64  `json:"xp"`
}

// TimedActionData is KindTimedAction.
type TimedActionData struct {
	// Kind is the action's type code ("travel", "education", "work_shift"...).
	Kind      string    `json:"kind"`
	RefType   string    `json:"ref_type"`
	RefID     string    `json:"ref_id"`
	State     string    `json:"state"`
	StartedAt time.Time `json:"started_at"`
	FinishAt  time.Time `json:"finish_at"`
}

// TravelData is a journey under way.
type TravelData struct {
	From       string    `json:"from"`
	To         string    `json:"to"`
	Mode       string    `json:"mode"`
	DepartedAt time.Time `json:"departed_at"`
	ArrivesAt  time.Time `json:"arrives_at"`
}

// WalkData is a walk across the city under way.
type WalkData struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	StartedAt time.Time `json:"started_at"`
	ArrivesAt time.Time `json:"arrives_at"`
}

// LocationData is KindLocation.
type LocationData struct {
	City  string `json:"city"`
	Place string `json:"place"`
	// Settlement is set when the city the player stands in is a founded
	// settlement (its id; the summary is KindSettlement).
	Settlement string      `json:"settlement"`
	Travel     *TravelData `json:"travel"`
	Walk       *WalkData   `json:"walk"`
}

// InboxData is KindInbox.
type InboxData struct {
	Unread int `json:"unread"`
	// Latest are the ids of the newest notices, newest first; each is a
	// KindNotice entity.
	Latest []string `json:"latest"`
}

// NoticeData is KindNotice: the notice as data (docs/adr/0039 section 8).
// A notice of a screen not carried as data yet has its kind and nothing to
// read; the client words it generically.
type NoticeData struct {
	Kind      string          `json:"kind"`
	Category  string          `json:"category"`
	Screen    string          `json:"screen"`
	View      json.RawMessage `json:"view"`
	CreatedAt time.Time       `json:"created_at"`
	Read      bool            `json:"read"`
}

// ResidenceData is KindResidence.
type ResidenceData struct {
	Settlement string `json:"settlement"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	Tier       string `json:"tier"`
	IsHead     bool   `json:"is_head"`
	Resident   bool   `json:"resident"`
}

// The kinds of viewer a settlement summary is cut for (ADR 0030 tiers).
const (
	ViewerHead   = "head"
	ViewerMember = "member"
	ViewerPublic = "public"
)

// TreasuryData is a settlement's treasury, for its members.
type TreasuryData struct {
	Currency string `json:"currency"`
	Balance  int64  `json:"balance"`
}

// ResearchData is the research under way.
type ResearchData struct {
	Code     string    `json:"code"`
	FinishAt time.Time `json:"finish_at"`
}

// SettlementData is KindSettlement, as this player may see it.
type SettlementData struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Tier     string `json:"tier"`
	Viewer   string `json:"viewer"`
	GridLots int    `json:"grid_lots"`
	// LayoutVersion is the version GET /settlements/{id}/layout answers this
	// viewer: a held layout with another version is fetched again.
	LayoutVersion string `json:"layout_version"`
	// Members only.
	Treasury  *TreasuryData `json:"treasury"`
	Knowledge int           `json:"knowledge"`
	Research  *ResearchData `json:"research"`
}

// FactionData is the player's faction.
type FactionData struct {
	ID   string `json:"id"`
	Rank string `json:"rank"`
}

// RelationsData is KindRelations.
type RelationsData struct {
	Friends  []string     `json:"friends"`
	Faction  *FactionData `json:"faction"`
	Presence string       `json:"presence"`
}
