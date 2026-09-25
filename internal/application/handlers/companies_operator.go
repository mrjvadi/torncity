package handlers

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
)

// ClosedByOperator is a company an operator dissolved (the console's
// "dissolve", audited with the operator's reason).
const ClosedByOperator = "operator"

// ErrCompanyNotActive is an operator's dissolution of a company already
// closed.
var ErrCompanyNotActive = errors.Sentinel(errors.CodeConflict, "handlers.ErrCompanyNotActive",
	"the company is already dissolved")

// OperatorClosing is what an operator's dissolution did.
type OperatorClosing struct {
	CompanyID  string `json:"company_id"`
	Code       string `json:"code"`
	DebtPaid   int64  `json:"debt_paid"`
	WrittenOff int64  `json:"written_off"`
	Tax        int64  `json:"tax"`
	Payout     int64  `json:"payout"`
	Staff      int    `json:"staff"`
}

// DissolveCompany closes a company on an operator's authority, inside the
// caller's unit of work, exactly as its owner's closing does — the debt paid
// as far as it goes, the rest (taxed) to its owner, the staff let go and
// told, its openings closed, the city told — with the operator's close
// reason. Shifts being worked refuse it, as they refuse the owner.
func DissolveCompany(ctx context.Context, tx application.Tx, cities application.CityRepository,
	policy application.PolicyReader, snap *content.Snapshot, meta envelope.Metadata, code string, now time.Time,
) (OperatorClosing, error) {
	h := &CompaniesHandler{cities: cities, policy: policy, msgs: keyTranslator{}}
	var out OperatorClosing
	found, err := tx.Companies().ByCode(ctx, playercode.Normalize(code))
	if err != nil {
		return out, err
	}
	// The staff's jobs first, then the company, as the owner's closing.
	staff, err := tx.Companies().Staff(ctx, found.ID)
	if err != nil {
		return out, err
	}
	for _, e := range staff {
		if _, err := tx.Employment().Current(ctx, e.PlayerID); err != nil && !isSentinel(err, application.ErrNotEmployed) {
			return out, err
		}
	}
	c, err := tx.Companies().Lock(ctx, found.ID)
	if err != nil {
		return out, err
	}
	if !c.Active() {
		return out, ErrCompanyNotActive
	}
	city, err := cities.ByID(ctx, c.CityID)
	if err != nil {
		return out, err
	}
	taxBPS, err := h.lever(ctx, *city, LeverCorporateTax)
	if err != nil {
		return out, err
	}
	books, acct, err := companyBooks(ctx, tx, *c)
	if err != nil {
		return out, err
	}
	closing, err := company.Close(books, int(taxBPS))
	if stderrors.Is(err, company.ErrShiftsRunning) {
		return out, errors.Conflict("shifts are being worked for the company; try again when they end")
	}
	if err != nil {
		return out, errors.Internal(err)
	}
	out = OperatorClosing{CompanyID: c.ID, Code: c.Code, DebtPaid: closing.DebtPaid.Minor(),
		WrittenOff: closing.WrittenOff.Minor(), Tax: closing.Payout.Tax.Minor(), Payout: closing.Payout.Net.Minor(),
		Staff: len(staff)}
	return out, h.dissolve(ctx, tx, meta, snap, c, acct, closing, ClosedByOperator, now)
}
