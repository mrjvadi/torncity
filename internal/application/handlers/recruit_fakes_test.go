package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// noRecruitment is a world where no company ever recruited a specialist: the
// fake transaction's Recruitment, so the handlers that count a company's
// specialists read none.
type noRecruitment struct{}

var _ application.RecruitRepository = noRecruitment{}

func (noRecruitment) Pool(context.Context, string, string, int, bool) (*application.SpecialistPool, error) {
	return nil, nil
}
func (noRecruitment) SavePool(context.Context, application.SpecialistPool) error   { return nil }
func (noRecruitment) EnsurePool(context.Context, application.SpecialistPool) error { return nil }
func (noRecruitment) Pending(context.Context, string, string, int, time.Time) (int64, error) {
	return 0, nil
}
func (noRecruitment) CreateCampaign(_ context.Context, c application.RecruitCampaign) (application.RecruitCampaign, error) {
	return c, nil
}
func (noRecruitment) Draft(context.Context, string) (*application.RecruitCampaign, error) {
	return nil, nil
}
func (noRecruitment) Campaign(context.Context, int64, bool) (*application.RecruitCampaign, error) {
	return nil, application.ErrCampaignNotFound
}
func (noRecruitment) CampaignByID(context.Context, string, bool) (*application.RecruitCampaign, error) {
	return nil, application.ErrCampaignNotFound
}
func (noRecruitment) SaveCampaign(context.Context, application.RecruitCampaign) error { return nil }
func (noRecruitment) Campaigns(context.Context, string, int) ([]application.RecruitCampaign, error) {
	return nil, nil
}
func (noRecruitment) Running(context.Context, string) (int, error)                { return 0, nil }
func (noRecruitment) RecordAdFee(context.Context, application.RecruitAdFee) error { return nil }
func (noRecruitment) AddCandidate(_ context.Context, c application.RecruitCandidate) (application.RecruitCandidate, bool, error) {
	return c, false, nil
}
func (noRecruitment) Candidate(context.Context, int64, bool) (*application.RecruitCandidate, error) {
	return nil, application.ErrCandidateNotFound
}
func (noRecruitment) Candidates(context.Context, string) ([]application.RecruitCandidate, error) {
	return nil, nil
}
func (noRecruitment) Decide(context.Context, string, string, string, time.Time) (bool, error) {
	return false, nil
}
func (noRecruitment) Withdraw(context.Context, string, time.Time) (int, error) { return 0, nil }
func (noRecruitment) Hire(_ context.Context, s application.NPCStaff) (application.NPCStaff, error) {
	return s, nil
}
func (noRecruitment) Staff(context.Context, string) ([]application.NPCStaff, error) { return nil, nil }
func (noRecruitment) Specialist(context.Context, int64) (*application.NPCStaff, error) {
	return nil, application.ErrSpecialistNotFound
}
func (noRecruitment) SaveStaff(context.Context, application.NPCStaff) error { return nil }
func (noRecruitment) RecordPayment(context.Context, application.NPCStaffPayment) (bool, error) {
	return false, nil
}
func (noRecruitment) PaidTotal(context.Context, string) (int64, error) { return 0, nil }
