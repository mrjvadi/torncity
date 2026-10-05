package handlers

import "testing"

func TestSeatClaimsPriority(t *testing.T) {
	c := seatClaims{Pool: 10, Shifts: 6, Teachers: 1, Shop: 1, Keepers: 2}
	// the standing posts are filled from the pool less the teachers, never from what the
	// shifts left
	if got := c.StaffFree(); got != 9 {
		t.Errorf("StaffFree = %d, want 9", got)
	}
	if got := c.Free(); got != 3 {
		t.Errorf("Free = %d, want 3", got)
	}
	if got := c.Available(); got != 0 {
		t.Errorf("Available = %d, want 0: the standing posts hold three seats", got)
	}
	if got := (seatClaims{Pool: 2, Shifts: 5}).Free(); got != 0 {
		t.Errorf("Free is never negative: %d", got)
	}
}
