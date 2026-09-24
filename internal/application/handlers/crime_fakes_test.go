package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// noCrime is the crime repository of a player who has never touched crime:
// never in jail, never in the middle of one. The handler fakes of other
// features return it from Tx.Crime, because travel, shifts and withdrawals
// ask it whether the player is detained.
type noCrime struct{}

var _ application.CrimeRepository = noCrime{}

func (noCrime) Profile(context.Context, string, application.CriminalProfile) (*application.CriminalProfile, error) {
	return nil, application.ErrPlayerNotFound
}
func (noCrime) SaveProfile(context.Context, application.CriminalProfile) error { return nil }
func (noCrime) ActiveAttempt(context.Context, string) (*application.CrimeAttempt, error) {
	return nil, application.ErrNoCrimeInProgress
}
func (noCrime) RecordAttempt(context.Context, application.CrimeAttempt) error  { return nil }
func (noCrime) ResolveAttempt(context.Context, application.CrimeAttempt) error { return nil }
func (noCrime) Attempt(context.Context, string) (*application.CrimeAttempt, error) {
	return nil, application.ErrCrimeNotFound
}
func (noCrime) RecentAttempts(context.Context, string, int) ([]application.CrimeAttempt, error) {
	return nil, nil
}
func (noCrime) LastAttempt(context.Context, string, string) (time.Time, error) {
	return time.Time{}, nil
}
func (noCrime) LastAttemptInCategory(context.Context, string, string) (time.Time, error) {
	return time.Time{}, nil
}
func (noCrime) Whereabouts(context.Context, string, string, time.Time) (string, string, error) {
	return "", "", nil
}
func (noCrime) Bystanders(context.Context, string, string, time.Time, time.Time, time.Time) ([]application.Bystander, error) {
	return nil, nil
}
func (noCrime) LockNPCProceeds(context.Context, time.Time, time.Time) (int64, error) { return 0, nil }
func (noCrime) AddNPCProceeds(context.Context, time.Time, int64, time.Time) error    { return nil }
func (noCrime) ActiveSentence(context.Context, string) (*application.JailSentence, error) {
	return nil, application.ErrNotJailed
}
func (noCrime) Sentence(context.Context, string) (*application.JailSentence, error) {
	return nil, application.ErrSentenceNotFound
}
func (noCrime) Jail(context.Context, application.JailSentence) error { return nil }
func (noCrime) ExtendSentence(context.Context, string, int64, time.Time, string) error {
	return nil
}
func (noCrime) EndSentence(context.Context, string, string, int64, string, time.Time) error {
	return nil
}
func (noCrime) FileReport(context.Context, application.CrimeReport) error { return nil }
func (noCrime) Report(context.Context, string) (*application.CrimeReport, error) {
	return nil, application.ErrReportNotFound
}
func (noCrime) ReportForCrime(context.Context, string) (*application.CrimeReport, error) {
	return nil, application.ErrReportNotFound
}
func (noCrime) ConcludeReport(context.Context, application.CrimeReport) error { return nil }
func (noCrime) ReportsBy(context.Context, string, int) ([]application.CrimeReport, error) {
	return nil, nil
}
