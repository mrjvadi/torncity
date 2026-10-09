package application

import "testing"

func TestApportionAddsUpAndFollowsTheWeights(t *testing.T) {
	cases := []struct {
		total   int64
		weights []int64
		want    []int64
	}{
		{2300, []int64{1000, 3000}, []int64{575, 1725}},
		{10, []int64{1, 1, 1}, []int64{4, 3, 3}}, // the remainder goes to the earlier index on a tie
		{0, []int64{5, 5}, []int64{0, 0}},
		{7, []int64{0, 0}, []int64{0, 0}},
		{5, []int64{0, 3}, []int64{0, 5}},
		{4000, []int64{1000, 3000}, []int64{1000, 3000}},
	}
	for _, c := range cases {
		got := Apportion(c.total, c.weights)
		var sum int64
		for i, g := range got {
			sum += g
			if g != c.want[i] {
				t.Errorf("Apportion(%d, %v) = %v, want %v", c.total, c.weights, got, c.want)
				break
			}
		}
		if c.total > 0 && sumWeights(c.weights) > 0 && sum != c.total {
			t.Errorf("Apportion(%d, %v) adds up to %d", c.total, c.weights, sum)
		}
	}
}

func sumWeights(w []int64) (n int64) {
	for _, x := range w {
		n += x
	}
	return
}

func TestLevyRefundIsAKnownReason(t *testing.T) {
	if !ReasonLevyRefund.Known() {
		t.Fatal("levy_refund is not in the closed set of ledger reasons")
	}
}
