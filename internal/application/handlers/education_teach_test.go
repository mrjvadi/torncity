package handlers

import "testing"

func TestTeacherWageIsAShareOfTheListedFeeWithAFloor(t *testing.T) {
	r := TeachRules{WageBPS: 6000, MinWage: 40, MaxStudents: 12}
	for _, tc := range []struct{ fee, want int64 }{{200, 120}, {1000, 600}, {10, 40}, {0, 40}} {
		if got := r.wage(tc.fee); got != tc.want {
			t.Errorf("wage(%d) = %d, want %d", tc.fee, got, tc.want)
		}
	}
	if (TeachRules{}).max() != 12 {
		t.Error("a zero class size falls back to 12")
	}
	if r.max() != 12 || (TeachRules{MaxStudents: 3}).max() != 3 {
		t.Error("the class size is the configured one")
	}
}
