package finance

// Insurance (docs/adr/0026 section 4). A policy is paid for by a premium
// every finance period — a fixed amount, or a share of the value insured —
// into its country's insurance fund, and pays a share of a real loss
// (a hospital treatment, war damage to the property insured) out of the
// same fund, up to a cap per claim.
//
// SOLVENCY. The fund is an account like any other: it never goes below zero.
// A claim pays what is due or what the fund holds, whichever is less, and the
// shortfall is recorded, never borrowed.

// Premium is one period's premium: fixed, plus bps of the insured value.
func Premium(fixed, bps, insured int64) int64 {
	return max(fixed, 0) + OfBPS(insured, bps)
}

// Claim is what a loss pays: due is coverBPS of the loss up to maxClaim (0 for
// no cap), paid is what the fund can pay of it.
func Claim(loss, coverBPS, maxClaim, fund int64) (due, paid int64) {
	due = OfBPS(loss, clamp(coverBPS, 0, BPSWhole))
	if maxClaim > 0 {
		due = min(due, maxClaim)
	}
	return due, min(due, max(fund, 0))
}

// ClaimDue is what a loss owes under a policy with a deductible (docs/adr/0074): coverBPS of the loss above the deductible (a share of
// the loss, deductibleBPS), up to maxClaim (0 for no cap). A deductible and coinsurance together are the standard cure for moral
// hazard: the insured always bears something.
func ClaimDue(loss, coverBPS, deductibleBPS, maxClaim int64) int64 {
	loss = max(loss, 0)
	above := loss - OfBPS(loss, clamp(deductibleBPS, 0, BPSWhole))
	due := OfBPS(above, clamp(coverBPS, 0, BPSWhole))
	if maxClaim > 0 {
		due = min(due, maxClaim)
	}
	return due
}

// ProRata shares what a fund holds among the claims due on it in one go: each is paid in full when the fund covers them all, else
// each gets its due times fund over the sum of dues, rounded down (so the fund is never overdrawn and the shortfall is the same share
// for everyone). The order of the result is the order of dues.
func ProRata(dues []int64, fund int64) []int64 {
	var total int64
	for _, d := range dues {
		total += max(d, 0)
	}
	fund = max(fund, 0)
	out := make([]int64, len(dues))
	for i, d := range dues {
		d = max(d, 0)
		switch {
		case total <= fund:
			out[i] = d
		case total > 0:
			out[i] = d * fund / total
		}
	}
	return out
}
