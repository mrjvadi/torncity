package screens

import (
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// A viewer whose home settlement has its own money reads every amount in it, at the live rate, with the
// SUP amount beside it; everyone else reads SUP only.
func TestFormatMoneyShowsTheLocalMoneyWithSUPBeside(t *testing.T) {
	c := ctx(t, "en", 0)
	if got := FormatMoney(c, 8); got != c.T("format.money", map[string]any{"amount": "8"}) {
		t.Errorf("without a local money the amount is SUP only: %q", got)
	}
	c.Money = &presentation.Money{Code: "MKP", Name: "Marco", R0: 10, XRefPPM: 1_000_000, RateNum: 10_000_000, RateDen: 1_000_000}
	got := FormatMoney(c, 8)
	if !strings.Contains(got, "80 Marco") || !strings.Contains(got, "8 SUP") {
		t.Errorf("8 SUP at 10 units per SUP reads 80 Marco with 8 SUP beside it: %q", got)
	}
	// the rate is live: a weaker unit buys more per SUP
	c.Money = &presentation.Money{Code: "MKP", Name: "Marco", R0: 10, XRefPPM: 800_000, RateNum: 10_000_000, RateDen: 800_000}
	if got := FormatMoney(c, 3); !strings.Contains(got, "38 Marco") {
		t.Errorf("3 SUP at 12.5 units per SUP reads 38 (nearest): %q", got)
	}
	if got := FormatMoney(c, -8); !strings.Contains(got, "-") {
		t.Errorf("a negative amount keeps its sign: %q", got)
	}
}
