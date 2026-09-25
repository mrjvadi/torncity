package handlers

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/recruit"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// A company's specialists at the settlement of its city
// (docs/adr/0027-specialist-recruitment.md): each is paid their period —
// salary and housing, all or nothing, from the money the company's running
// shifts have not reserved — or goes unpaid; is weighed against the market
// today; completes, or runs out, their contract; and leaves when unpaid or
// underpaid too long. The settlement runs once per city period (its clock
// row), and a period's pay once per specialist (npc_staff_payments' key),
// so a specialist is never paid twice for one period.

// specialistsAt is how many specialists work for a company now.
func specialistsAt(ctx context.Context, tx application.Tx, companyID string) (int, error) {
	staff, err := tx.Recruitment().Staff(ctx, companyID)
	return len(staff), err
}

// paySpecialists settles a company's specialists for period periodNo;
// balance is its treasury as the settlement has it now and reserved what
// its running shifts hold. It returns what left the treasury.
func (h *CompaniesHandler) paySpecialists(ctx context.Context, tx application.Tx, meta envelope.Metadata,
	snap *content.Snapshot, c application.Company, treasuryID string, balance money.Amount, reserved, periodNo int64,
	now time.Time,
) (int64, error) {
	def, ok := recruitmentOf(snap)
	if !ok {
		return 0, nil
	}
	staff, err := tx.Recruitment().Staff(ctx, c.ID)
	if err != nil || len(staff) == 0 {
		return 0, err
	}
	m := jobMarket{def: def, snap: snap, scale: h.scale, now: now}
	rules := def.Staff.Rules()
	free := max(balance.Minor()-reserved, 0)
	var spent int64
	for _, s := range staff {
		expected, err := expectedNow(ctx, tx, m, c, s)
		if err != nil {
			return spent, err
		}
		due := s.Due()
		out := recruit.Settle(standingOf(s), rules, due > 0 && free >= due, expected)
		if out.Pay {
			txID, err := postRef(ctx, tx.Ledger(), application.ReasonSpecialistSalary, application.NPCStaffReference, s.ID,
				treasuryID, application.SystemSinkAccountID, money.FromMinor(due), now)
			if err != nil {
				return spent, err
			}
			fresh, err := tx.Recruitment().RecordPayment(ctx, application.NPCStaffPayment{StaffID: s.ID, PeriodNo: periodNo,
				CompanyID: c.ID, Amount: due, LedgerTransactionID: txID, PaidAt: now})
			if err != nil {
				return spent, err
			}
			if !fresh {
				return spent, errors.Internal(stderrors.New("handlers: a specialist was paid twice for one period"))
			}
			free -= due
			spent += due
		}
		next := out.Next
		s.Served, s.Expiring, s.UnpaidRun, s.UnderpaidRun, s.UpdatedAt = next.Served, next.Expiring, next.UnpaidRun,
			next.UnderpaidRun, now
		event := map[string]any{"name_seed": s.NameSeed, "skill": s.Skill, "level": s.Level, "staff_no": s.No}
		if !out.Pay && out.Leave == "" {
			if err := appendCompanyEvent(ctx, tx, meta, "recruit", c.ID,
				recruitEvent(c, screens.RecruitNoticeUnpaid, event)); err != nil {
				return spent, err
			}
		}
		if out.Completed {
			// The phantom shares vest: paid at today's price, if the free
			// money covers them; otherwise they are forfeited.
			value, err := shareValue(ctx, tx, c, s.Shares)
			if err != nil {
				return spent, err
			}
			if value > 0 && free >= value {
				if _, err := postRef(ctx, tx.Ledger(), application.ReasonSpecialistEquity, application.NPCStaffReference,
					s.ID, treasuryID, application.SystemSinkAccountID, money.FromMinor(value), now); err != nil {
					return spent, err
				}
				s.EquityPaid += value
				free -= value
				spent += value
				event["amount"] = value
			}
			if err := appendCompanyEvent(ctx, tx, meta, "recruit", c.ID,
				recruitEvent(c, screens.RecruitNoticeCompleted, event)); err != nil {
				return spent, err
			}
		}
		if out.Leave != "" {
			s.Status, s.LeaveReason, s.LeftAt = application.StaffLeft, out.Leave, &now
			event["reason"] = out.Leave
			if err := appendCompanyEvent(ctx, tx, meta, "recruit", c.ID,
				recruitEvent(c, screens.RecruitNoticeLeft, event)); err != nil {
				return spent, err
			}
		}
		if err := tx.Recruitment().SaveStaff(ctx, s); err != nil {
			return spent, err
		}
	}
	return spent, nil
}

// releaseSpecialists ends a closing company's recruitment: its specialists
// leave, its campaigns stop, their waiting candidates step aside.
func releaseSpecialists(ctx context.Context, tx application.Tx, companyID string, now time.Time) error {
	staff, err := tx.Recruitment().Staff(ctx, companyID)
	if err != nil {
		return err
	}
	for _, s := range staff {
		s.Status, s.LeaveReason, s.LeftAt, s.UpdatedAt = application.StaffLeft, recruit.LeaveCompanyClosed, &now, now
		if err := tx.Recruitment().SaveStaff(ctx, s); err != nil {
			return err
		}
	}
	camps, err := tx.Recruitment().Campaigns(ctx, companyID, 100)
	if err != nil {
		return err
	}
	for _, camp := range camps {
		if camp.Status != application.CampaignRunning && camp.Status != application.CampaignDraft {
			continue
		}
		if _, err := tx.Recruitment().Withdraw(ctx, camp.ID, now); err != nil {
			return err
		}
		if camp.Status == application.CampaignDraft {
			camp.PostedAt = &now
		}
		camp.Status, camp.EndedAt, camp.ActionID, camp.NextCheckAt, camp.UpdatedAt = application.CampaignCancelled,
			&now, "", nil, now
		if err := tx.Recruitment().SaveCampaign(ctx, camp); err != nil {
			return err
		}
	}
	return nil
}
