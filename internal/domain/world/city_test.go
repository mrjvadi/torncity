package world

import (
	"errors"
	"math"
	"testing"
)

func TestCityValidate(t *testing.T) {
	valid := City{ID: "id-1", Code: "alpha", Name: "Alpha", TaxRateBPS: 500, CostOfLiving: 1000, Population: 10}

	tests := []struct {
		name    string
		mutate  func(City) City
		wantErr error
	}{
		{"a well formed city", func(c City) City { return c }, nil},
		{"tax of zero is allowed", func(c City) City { c.TaxRateBPS = 0; return c }, nil},
		{"tax of exactly 100% is allowed", func(c City) City { c.TaxRateBPS = BasisPointsScale; return c }, nil},
		{"a nameless city still loads", func(c City) City { c.Name = ""; return c }, nil},
		{"no code", func(c City) City { c.Code = ""; return c }, ErrMissingCityCode},
		{"negative tax", func(c City) City { c.TaxRateBPS = -1; return c }, ErrInvalidTaxRate},
		{"tax above 100%", func(c City) City { c.TaxRateBPS = BasisPointsScale + 1; return c }, ErrInvalidTaxRate},
		{"negative cost of living", func(c City) City { c.CostOfLiving = -1; return c }, ErrInvalidCostOfLiving},
		{"negative population", func(c City) City { c.Population = -1; return c }, ErrInvalidPopulation},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.mutate(valid).Validate(); !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestTaxOn(t *testing.T) {
	tests := []struct {
		name   string
		bps    int
		amount int64
		want   int64
		why    string
	}{
		{
			name: "no tax at all", bps: 0, amount: 1_000_000, want: 0,
		},
		{
			name: "a whole percentage", bps: 1000, amount: 5_000, want: 500,
		},
		{
			name: "everything", bps: BasisPointsScale, amount: 1234, want: 1234,
			why: "a 100% rate takes exactly the amount and not one unit more",
		},
		{
			name: "an exact fraction, nothing to round", bps: 750, amount: 1_000, want: 75,
		},
		{
			name: "half a unit is dropped", bps: 750, amount: 100, want: 7,
			why: "7.5 truncates DOWN to 7: the half unit stays with the payer",
		},
		{
			name: "almost a whole unit is still dropped", bps: 9999, amount: 1, want: 0,
			why: "0.9999 truncates to 0 rather than rounding up to 1",
		},
		{
			name: "one unit short of the first whole unit", bps: 1, amount: 9_999, want: 0,
		},
		{
			name: "exactly the first whole unit", bps: 1, amount: 10_000, want: 1,
		},
		{
			name: "an awkward amount and an awkward rate", bps: 733, amount: 12_345, want: 904,
			why: "12345*733/10000 = 904.87... truncates to 904",
		},
		{
			name: "a remainder either side of the split", bps: 5_000, amount: 10_001, want: 5_000,
			why: "5000.5 truncates to 5000, proving the two halves of the split arithmetic recombine",
		},
		{
			name: "tax on nothing", bps: 2_500, amount: 0, want: 0,
		},
		{
			name: "a negative amount truncates toward zero too", bps: 750, amount: -100, want: -7,
			why: "-7.5 becomes -7, not -8: truncation is toward zero in both directions, " +
				"so reversing an entry reverses its tax exactly",
		},
		{
			name: "a rate above 100% is clamped to 100%", bps: 50_000, amount: 900, want: 900,
			why: "corrupt content must not make a city take more than the whole amount",
		},
		{
			name: "a negative rate is clamped to zero", bps: -500, amount: 900, want: 0,
		},
		{
			name: "the largest amount at the full rate does not overflow",
			bps:  BasisPointsScale, amount: math.MaxInt64, want: math.MaxInt64,
			why: "the naive amount*bps would have overflowed long before here",
		},
		{
			name: "the largest amount at half rate",
			bps:  5_000, amount: math.MaxInt64, want: 4_611_686_018_427_387_903,
		},
		{
			name: "the smallest amount does not overflow",
			bps:  BasisPointsScale, amount: math.MinInt64, want: math.MinInt64,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := City{Code: "alpha", TaxRateBPS: tt.bps}
			if got := c.TaxOn(tt.amount); got != tt.want {
				t.Errorf("TaxOn(%d) at %d bps = %d, want %d (%s)", tt.amount, tt.bps, got, tt.want, tt.why)
			}
		})
	}
}

// TestTaxNeverExceedsAmount is the property the rounding decision exists to
// guarantee: whatever the rate and whatever the amount, a city cannot take
// more than was there to be taxed.
func TestTaxNeverExceedsAmount(t *testing.T) {
	rates := []int{0, 1, 17, 250, 999, 5_000, 9_999, BasisPointsScale}
	amounts := []int64{0, 1, 7, 99, 100, 9_999, 10_000, 10_001, 123_456_789, math.MaxInt64}

	for _, bps := range rates {
		for _, amount := range amounts {
			c := City{Code: "alpha", TaxRateBPS: bps}
			tax := c.TaxOn(amount)
			if tax < 0 {
				t.Errorf("TaxOn(%d) at %d bps = %d, a negative tax on a positive amount", amount, bps, tax)
			}
			if tax > amount {
				t.Errorf("TaxOn(%d) at %d bps = %d, which is more than the amount", amount, bps, tax)
			}
		}
	}
}

// TestTaxMatchesExactArithmeticInSafeRange checks the split-multiplication
// form against the obvious one, over amounts small enough that the obvious one
// cannot overflow. The split form exists only to survive large amounts, so it
// must agree with the simple version everywhere else.
func TestTaxMatchesExactArithmeticInSafeRange(t *testing.T) {
	for bps := 0; bps <= BasisPointsScale; bps += 137 {
		c := City{Code: "alpha", TaxRateBPS: bps}
		for amount := int64(-5_000); amount <= 5_000; amount += 7 {
			want := amount * int64(bps) / BasisPointsScale
			if got := c.TaxOn(amount); got != want {
				t.Fatalf("TaxOn(%d) at %d bps = %d, want %d", amount, bps, got, want)
			}
		}
	}
}
