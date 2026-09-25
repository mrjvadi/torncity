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
