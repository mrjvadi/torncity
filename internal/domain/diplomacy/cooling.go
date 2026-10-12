package diplomacy

import "time"

// CoolingUntil answers whether breaker may not yet declare a war or a raid on other, because it ended an alliance or a
// non-aggression pact with other less than cooling ago (docs/adr/0076 7.2). It is the first instant the breaker may act, and true while
// the cooling runs. A treaty the other party ended, or one that lapsed, never cools anyone; only the party that broke it does.
func CoolingUntil(treaties []Treaty, types map[string]TreatyType, breaker, other string, cooling time.Duration, now time.Time) (time.Time, bool) {
	if breaker == other || cooling <= 0 {
		return time.Time{}, false
	}
	var until time.Time
	for _, t := range treaties {
		if t.Status != Terminated || t.EndedAt.IsZero() || t.EndedBy != breaker || !t.Member(breaker) || !t.Member(other) {
			continue
		}
		ty, ok := types[t.Kind]
		if !ok || !(ty.MutualDefence || ty.NonAggression) {
			continue
		}
		if end := t.EndedAt.Add(cooling); end.After(until) {
			until = end
		}
	}
	return until, until.After(now)
}

// Allied reports whether a and b hold a treaty in force whose type carries the beacon relay.
func Allied(treaties []Treaty, types map[string]TreatyType, a, b string, now time.Time) bool {
	return Partners(treaties, types, a, b, now, func(t TreatyType) bool { return t.BeaconRelay })
}

// MayPass reports whether a and b hold a treaty in force that lets either lay roads and rails over the other's claims.
func MayPass(treaties []Treaty, types map[string]TreatyType, a, b string, now time.Time) bool {
	return Partners(treaties, types, a, b, now, func(t TreatyType) bool { return t.Passage })
}
