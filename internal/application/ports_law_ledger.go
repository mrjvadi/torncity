package application

// The accounts and the ledger reasons of police, courts, the hidden board, laundering and customs (docs/adr/0078 3.15). Money only moves between
// accounts, except the fees and the food of the prisoners, which are sinks.

// The accounts of ADR 0078: the dirty balance of a player (the take of illicit trade and bribes; it pays nothing legal), the escrow of a trade on the
// hidden board, and the police purse of a settlement (its share of what is forfeited, spent on informants and audits).
const (
	AccountPlayerDirty AccountKind = "player_dirty"
	AccountEscrowBlack AccountKind = "escrow_black"
	AccountPolicePurse AccountKind = "police_purse"
)

// The reasons.
const (
	ReasonCourtFee           Reason = "court_fee"
	ReasonCourtFeeRefund     Reason = "court_fee_refund"
	ReasonAppealFee          Reason = "appeal_fee"
	ReasonAppealRefund       Reason = "appeal_refund"
	ReasonFineVerdict        Reason = "fine_verdict"
	ReasonFineRefund         Reason = "fine_refund"
	ReasonRestitutionVerdict Reason = "restitution_verdict"
	ReasonForfeiture         Reason = "forfeiture"
	ReasonForfeiturePolice   Reason = "forfeiture_police_share"
	ReasonBribe              Reason = "bribe"
	ReasonBribeForfeit       Reason = "bribe_forfeit"
	ReasonInformantPay       Reason = "informant_pay"
	ReasonInformantRetaliate Reason = "informant_retaliation"
	ReasonJailFood           Reason = "jail_food"
	ReasonDefenderFee        Reason = "defender_fee"
	ReasonAuditFee           Reason = "audit_fee"
	ReasonBlackListingFee    Reason = "black_listing_fee"
	ReasonEscrowHold         Reason = "escrow_hold"
	ReasonEscrowRelease      Reason = "black_trade"
	ReasonEscrowFee          Reason = "escrow_fee"
	ReasonEscrowRefund       Reason = "escrow_refund"
	ReasonEscrowScam         Reason = "escrow_scam"
	ReasonLaunder            Reason = "launder"
	ReasonLaunderFee         Reason = "launder_fee"
	ReasonCustomsDuty        Reason = "customs_duty"
	ReasonCustomsPenalty     Reason = "customs_penalty"
)

func init() {
	for _, r := range []Reason{ReasonCourtFee, ReasonCourtFeeRefund, ReasonAppealFee, ReasonAppealRefund, ReasonFineVerdict, ReasonFineRefund, ReasonRestitutionVerdict,
		ReasonForfeiture, ReasonForfeiturePolice, ReasonBribe, ReasonBribeForfeit, ReasonInformantPay, ReasonInformantRetaliate, ReasonJailFood, ReasonDefenderFee,
		ReasonAuditFee, ReasonBlackListingFee, ReasonEscrowHold, ReasonEscrowRelease, ReasonEscrowFee, ReasonEscrowRefund, ReasonEscrowScam, ReasonLaunder,
		ReasonLaunderFee, ReasonCustomsDuty, ReasonCustomsPenalty} {
		knownReasons[r] = struct{}{}
	}
}
