package application

import "time"

// SettlementFacts is what a settlement holds right now, read for the
// client's per-viewer overlay and goal (clientapi, docs/adr/0034). It is a
// read model: nothing in a command decides from it.
type SettlementFacts struct {
	Treasury int64
	// StockUnits is the village stock by item and StockUsed its total.
	StockUnits map[string]int64
	StockUsed  int64
	// Shifts counts the shifts working now, by building id.
	Shifts map[string]int
	// OpenJobs is the building ids that have an open labour job.
	OpenJobs map[string]bool
	// Owned is the knowledge held (code -> how it was acquired).
	Owned       map[string]string
	LiteracyBPS int
	Residents   int64
	// Election is the open election of the settlement's jurisdiction, nil
	// for none.
	Election *ElectionCalendar
}

// ElectionCalendar is an open election's dates.
type ElectionCalendar struct {
	Office          string
	OpensAt         time.Time
	CandidacyEndsAt time.Time
	VotingEndsAt    time.Time
}

// MissionProgress is a mission a player took and has not finished.
type MissionProgress struct {
	Code string
	// Progress is one count per objective, in the content's order.
	Progress []int64
}
