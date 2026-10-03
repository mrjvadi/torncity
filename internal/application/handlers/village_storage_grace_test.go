package handlers

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// A store that stood before the keeper rule keeps counting its room for the grace
// period; one built after the rule, or once the grace is over, does not.
func TestStoreGraceKeepsOldStoresCounting(t *testing.T) {
	snap := shippedSnapshot(t)
	rule := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	rules := StorageRules{GraceFrom: rule, GraceDays: 14}
	old := rule.AddDate(0, 0, -30)
	late := rule.AddDate(0, 0, 2)
	stores := storeBuildings(snap, []application.SettlementBuildingInstance{
		{ID: "old", TypeCode: "storehouse", Status: "complete", CompletedAt: &old},
		{ID: "new", TypeCode: "storehouse", Status: "complete", CompletedAt: &late},
	}, rules)
	if len(stores) != 2 {
		t.Fatalf("stores = %+v", stores)
	}
	byID := map[string]storeBuilding{}
	for _, s := range stores {
		byID[s.ID] = s
	}
	if !byID["old"].counts(rule.AddDate(0, 0, 13)) {
		t.Error("an old store without a keeper must count inside the grace")
	}
	if byID["old"].counts(rule.AddDate(0, 0, 15)) {
		t.Error("an old store without a keeper must stop counting once the grace is over")
	}
	if byID["new"].counts(rule.AddDate(0, 0, 3)) {
		t.Error("a store built after the rule has no grace")
	}
	if !(StorageRules{GraceFrom: rule}).graceUntil(old).IsZero() {
		t.Error("zero grace days switches the grace off")
	}
}
