package postgres

// The read paths of the client API that run outside a unit of work: which
// settlement a player belongs to, what a settlement's lot grid holds. They
// are the same repositories a command's transaction reads through, bound to
// the shared pool instead of a transaction, so a client and the game see one
// set of rows and one set of queries.

// NewSettlementReader returns the settlement repository over the shared
// pool. Never call it inside a transaction: use the unit of work's own
// Tx.Settlements there (the ambient-transaction rule).
func NewSettlementReader(p *Pool) *SettlementRepository { return &SettlementRepository{q: p.shared()} }

// NewSettlementBuildingReader is the same for a settlement's buildings.
func NewSettlementBuildingReader(p *Pool) *SettlementBuildingRepository {
	return &SettlementBuildingRepository{q: p.shared()}
}

// isUUID reports whether s is a canonical 8-4-4-4-12 hex UUID, so a client's
// path segment can be told apart from an id before it reaches a uuid cast
// (which would fail the statement, not just find nothing).
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		c := s[i]
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
