package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/diplomacy"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// The border tariff (docs/adr/0024-property-and-politics.md): a sale of goods
// from a company or a market seller of one country to a buyer of another
// pays the importing country's tariff (country.border_tariff, through the
// resolver), less the largest discount a treaty in force between the two
// grants (diplomacy.yml tariff_discount_bps). It is withheld from the
// seller's proceeds — like a fee, so a trade the buyer could pay is never
// refused for it — and paid into the importing country's national treasury,
// once per trade.

// LeverBorderTariff is the importing country's tariff.
const LeverBorderTariff = "country.border_tariff"

// tariffQuote is the tariff one sale owes.
type tariffQuote struct {
	importer, exporter  string
	value, rate, amount int64
}

// quoteTariff works out the tariff on goods worth value that a buyer in
// importer bought from a seller in exporter. Within one country, or where
// either is none, there is none.
func quoteTariff(ctx context.Context, tx application.Tx, policy application.PolicyReader, snap *content.Snapshot,
	importer, exporter string, value int64, now time.Time,
) (tariffQuote, error) {
	q := tariffQuote{importer: importer, exporter: exporter, value: value}
	if importer == "" || exporter == "" || importer == exporter || value <= 0 || policy == nil {
		return q, nil
	}
	base, err := policy.Get(ctx, importer, LeverBorderTariff)
	if err != nil || base.Value <= 0 {
		return q, err
	}
	treaties, err := tx.Diplomacy().TreatiesBetween(ctx, importer, exporter)
	if err != nil {
		return q, err
	}
	rules := make([]diplomacy.Treaty, 0, len(treaties))
	for _, t := range treaties {
		rules = append(rules, t.Rule())
	}
	types := map[string]diplomacy.TreatyType{}
	for _, t := range snap.TreatyTypes() {
		types[t.Code] = t.Type()
	}
	q.rate = diplomacy.Tariff(base.Value, rules, types, importer, exporter, now)
	q.amount = value * q.rate / 10000
	return q, nil
}

// charge pays the tariff from an account into the importing country's
// national treasury and records it against the trade.
func (q tariffQuote) charge(ctx context.Context, tx application.Tx, ids IDGenerator, refType, refID, from string,
	now time.Time,
) error {
	if q.amount <= 0 {
		return nil
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountStateTreasury, q.importer)
	if err != nil {
		return err
	}
	if _, err := postRef(ctx, tx.Ledger(), application.ReasonBorderTariff, refType, refID, from, treasury.ID,
		money.FromMinor(q.amount), now); err != nil {
		return err
	}
	return tx.Diplomacy().RecordTariff(ctx, application.BorderTariff{ID: ids.NewID(), ReferenceType: refType,
		ReferenceID: refID, ImporterID: q.importer, ExporterID: q.exporter, Value: q.value, RateBPS: q.rate,
		Tariff: q.amount, At: now})
}
