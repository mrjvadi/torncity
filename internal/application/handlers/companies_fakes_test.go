package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// noCompanies is a world without player companies, for the handler tests
// that run against fakes: every list is empty and every lookup finds
// nothing. Companies themselves are tested against PostgreSQL
// (tests/companies_integration_test.go).
type noCompanies struct{}

var _ application.CompanyRepository = noCompanies{}

func (noCompanies) Create(context.Context, application.Company, application.CompanyShareholder) error {
	return application.ErrCompanyNotFound
}
func (noCompanies) ByID(context.Context, string) (*application.Company, error) {
	return nil, application.ErrCompanyNotFound
}
func (noCompanies) ByCode(context.Context, string) (*application.Company, error) {
	return nil, application.ErrCompanyNotFound
}
func (noCompanies) Lock(context.Context, string) (*application.Company, error) {
	return nil, application.ErrCompanyNotFound
}
func (noCompanies) InCity(context.Context, string) ([]application.Company, error) { return nil, nil }
func (noCompanies) LockActiveInCity(context.Context, string) ([]application.Company, error) {
	return nil, nil
}
func (noCompanies) Of(context.Context, string) ([]application.Company, error) { return nil, nil }
func (noCompanies) OwnedCount(context.Context, string) (int, error)           { return 0, nil }
func (noCompanies) All(context.Context, int) ([]application.Company, error)   { return nil, nil }
func (noCompanies) Save(context.Context, application.Company) error {
	return application.ErrCompanyNotFound
}
func (noCompanies) Reserved(context.Context, string) (int64, error) { return 0, nil }
func (noCompanies) Staff(context.Context, string) ([]application.CompanyEmployee, error) {
	return nil, nil
}
func (noCompanies) Shareholders(context.Context, string) ([]application.CompanyShareholder, error) {
	return nil, nil
}
func (noCompanies) Activity(context.Context, string, time.Time, time.Time) (int, int64, error) {
	return 0, 0, nil
}
func (noCompanies) PostOpening(_ context.Context, o application.CompanyOpening) (application.CompanyOpening, error) {
	return o, application.ErrCompanyNotFound
}
func (noCompanies) Opening(context.Context, int64, bool) (*application.CompanyOpening, error) {
	return nil, application.ErrOpeningNotFound
}
func (noCompanies) OpeningByID(context.Context, string) (*application.CompanyOpening, error) {
	return nil, application.ErrOpeningNotFound
}
func (noCompanies) Openings(context.Context, string) ([]application.CompanyOpening, error) {
	return nil, nil
}
func (noCompanies) CityOpenings(context.Context, string) ([]application.CityOpening, error) {
	return nil, nil
}
func (noCompanies) SaveOpening(context.Context, application.CompanyOpening) error { return nil }
func (noCompanies) Apply(_ context.Context, a application.CompanyApplication) (application.CompanyApplication, error) {
	return a, application.ErrOpeningNotFound
}
func (noCompanies) Application(context.Context, int64) (*application.CompanyApplication, error) {
	return nil, application.ErrApplicationNotFound
}
func (noCompanies) Pending(context.Context, string) ([]application.CompanyApplication, error) {
	return nil, nil
}
func (noCompanies) Decide(context.Context, string, string, string, time.Time) (bool, error) {
	return false, nil
}
func (noCompanies) WithdrawPlayerApplications(context.Context, string, time.Time) (int, error) {
	return 0, nil
}
func (noCompanies) WithdrawCompanyApplications(context.Context, string, time.Time) (int, error) {
	return 0, nil
}
func (noCompanies) CloseOpenings(context.Context, string, time.Time) error { return nil }
func (noCompanies) MarketClock(context.Context, string, time.Time) (*application.CompanyMarketClock, error) {
	return nil, application.ErrNoMarketClock
}
func (noCompanies) SaveMarketClock(context.Context, application.CompanyMarketClock) error { return nil }
func (noCompanies) NextSettlement(context.Context, string) (*time.Time, error)            { return nil, nil }
func (noCompanies) RecordMarketPeriod(context.Context, application.CompanyMarketPeriod) (bool, error) {
	return false, nil
}
func (noCompanies) RecordPeriod(context.Context, application.CompanyPeriod) error { return nil }
func (noCompanies) LastPeriod(context.Context, string) (*application.CompanyPeriod, error) {
	return nil, application.ErrNoCompanyPeriod
}
