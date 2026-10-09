package application

import (
	"context"
	"time"
)

// The port of the daily services (migration 0137; docs/adr/0052): the days a settlement's watch posts and inns were
// judged. The rules are handlers/village_service.go.

const (
	// ReasonServiceWage pays the staff of the posts that were open a day's wage from the treasury into the sink.
	ReasonServiceWage Reason = "service_wage"
	// ItemServiceUpkeep is an end: the firewood, bread and water an open post used up on its day.
	ItemServiceUpkeep ItemReason = "service_upkeep"
)

func init() {
	knownReasons[ReasonServiceWage] = struct{}{}
	itemReasons[ItemServiceUpkeep] = true
}

// ServiceDayReference is the reference type of the ledger and item-journal legs of a service day.
const ServiceDayReference = "service_days"

// The daily services (the `produces.service` of a function whose `produces.daily` is set).
const (
	ServiceSecurity = "local_security"
	ServiceLodging  = "lodging_and_tea"
)

// Why a post stood idle for a day.
const (
	ServiceIdleNoStaff    = "no_staff"
	ServiceIdleNoWage     = "no_wage"
	ServiceIdleNoSupplies = "no_supplies"
)

// ServicePost is one post's day.
type ServicePost struct {
	BuildingID string
	Service    string
	Held       bool
	Idle       string
	Staff      int64
	Wage       int64
	Used       map[string]int64
}

// ServiceDay is one judged local day of a settlement's services.
type ServiceDay struct {
	SettlementID string
	Day          int64
	Staff, Wage  int64
	WageTx       string
	At           time.Time
	Posts        []ServicePost
}

// Held is how many posts were open.
func (d ServiceDay) Held() int64 {
	var n int64
	for _, p := range d.Posts {
		if p.Held {
			n++
		}
	}
	return n
}

// HeldPost reports whether the building's post was open that day.
func (d ServiceDay) HeldPost(buildingID string) bool {
	for _, p := range d.Posts {
		if p.BuildingID == buildingID {
			return p.Held
		}
	}
	return false
}

// HeldService reports whether any post of the service was open that day.
func (d ServiceDay) HeldService(service string) bool {
	for _, p := range d.Posts {
		if p.Held && p.Service == service {
			return true
		}
	}
	return false
}

// ServiceDayRepository persists the days. Reach it through Tx.ServiceDays.
type ServiceDayRepository interface {
	Day(ctx context.Context, settlementID string, day int64) (*ServiceDay, error)
	Last(ctx context.Context, settlementID string) (*ServiceDay, error)
	// RecordDay is the fence: it writes the day and its posts, or reports false when the day already has a row.
	RecordDay(ctx context.Context, d ServiceDay) (bool, error)
}
