package economy

// Bank callback addresses. Amounts travel in them as whole minor units and a
// payee as their PUBLIC code, never a record id: an address is a hint the core
// re-checks, and a code is short enough to leave room for an amount and a
// one-time token inside Telegram's 64 bytes.
const (
	AddrBank     = "bank:show"
	AddrDeposit  = "bank:deposit"
	AddrWithdraw = "bank:withdraw"
	AddrPay      = "bank:pay"
	AddrPaySend  = "bank:pay.send"
)

// Payment methods as they travel in an address and are read back by the
// handler. They are the domain's values (bank.MethodCash, bank.MethodCard),
// restated here because a screen does not import the domain's rules.
const (
	PayCash = "cash"
	PayCard = "card"
)

// AmountOption is one quick-amount button: the amount it moves and the
// one-time token that makes a second press of the same button a replay. All
// marks the button for everything the player can move, which reads «همه».
type AmountOption struct {
	Amount int64
	Nonce  string
	All    bool
}

// BankView is the bank screen.
type BankView struct {
	// CityCode and City are where the player is, or, on the road, empty.
	CityCode string
	City     string
	// Travelling and NoCity explain why the bank is closed.
	Travelling bool
	NoCity     bool
	// Jailed says the player is in jail: they may deposit, not withdraw.
	Jailed bool

	Cash int64
	Bank int64

	// WithdrawalFeeBPS is the city's withdrawal fee, in basis points.
	WithdrawalFeeBPS int64

	// Deposits and Withdrawals are the quick amounts, round sums the player
	// can actually move — a withdrawal's fee included, so none of them can
	// fail for want of money — and «all of it».
	Deposits    []AmountOption
	Withdrawals []AmountOption
	// CanDeposit and CanWithdraw say whether any amount at all can be moved
	// that way, which is when «✏️ مبلغ دلخواه» is offered for it.
	CanDeposit  bool
	CanWithdraw bool

	// Notice is the outcome of what the player just did, as a code
	// (deposited, withdrew, withdrew_fee) with its sums in NoticeArgs (minor
	// units); empty on a plain visit.
	Notice     string
	NoticeArgs map[string]any
}

// PayView is the screen for paying one player.
type PayView struct {
	// PayeeName is the payee's display name, empty when they have none
	// worth showing; PayeeCode their public code, which addresses them.
	PayeeName string
	PayeeCode string

	// Together says the two are face to face, in CityCode/City, so cash can
	// change hands.
	Together bool
	CityCode string
	City     string

	// PayerCityCode and PayerCity are the city whose bank charges the card
	// fee; CardFeeBPS is that fee.
	PayerCityCode string
	PayerCity     string
	CardFeeBPS    int64

	Cash int64
	Bank int64

	// CashOptions and CardOptions are the quick amounts each way, none more
	// than the player can pay that way (the card's fee included).
	CashOptions []AmountOption
	CardOptions []AmountOption
	// CanCash and CanCard say whether any amount can be paid that way, which
	// is when «✏️ مبلغ دلخواه» is offered for it.
	CanCash bool
	CanCard bool

	// Origin is the group the payment was started in, carried to the
	// confirmation so the group can be told it was made. Empty from a
	// private chat.
	Origin string

	// Notice explains a refusal that brought the player back here, as a code
	// (not_together, short_cash, short_bank) with its data in NoticeArgs;
	// empty on a plain visit.
	Notice     string
	NoticeArgs map[string]any
}

// PayConfirmView is the last look before money leaves.
type PayConfirmView struct {
	PayeeName string
	PayeeCode string
	Method    string
	Amount    int64
	Fee       int64
	Total     int64
	// After is what is left where the money comes from — the cash for cash,
	// the bank for a card — once it is paid.
	After int64
	// Nonce makes the confirm button single-use: a second press is a
	// replay, not a second payment.
	Nonce string
	// Origin is the group the payment was started in, if any; see PayView.
	Origin string
}

// PaySentView is the outcome of a payment, for the payer.
type PaySentView struct {
	PayeeName string
	PayeeCode string
	Method    string
	Amount    int64
	Fee       int64
	// Held says the watch held the payment for review (docs/adr/0023): the
	// money has left the payer and waits in their escrow.
	Held bool
}
