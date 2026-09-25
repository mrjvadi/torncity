package panel

import (
	"context"
	"net/http"
	"sort"
	"time"
)

// Time series for the console's charts. Each is one constant statement that
// answers (day, key, value) rows from $1 (the range's start) on, days in
// Tehran time, the game's clock; a series of one thing (a company's share
// price) takes that thing's id as $2.

// SeriesDef is one series.
type SeriesDef struct {
	SQL string
	// Scope, when set, is the scope kind the series is of, required.
	Scope string
	// Unit is how the client formats the values: int, money, bps.
	Unit string
}

// day is the SQL for a timestamp's day on the game's clock.
func day(col string) string { return "(" + col + " AT TIME ZONE 'Asia/Tehran')::date" }

var seriesCatalogue = map[string]SeriesDef{
	"players.new": {Unit: TInt, SQL: `SELECT ` + day("created_at") + ` AS day, 'new' AS key, count(*) AS value
		FROM players WHERE created_at >= $1 GROUP BY 1`},
	"crimes.outcomes": {Unit: TInt, SQL: `SELECT ` + day("started_at") + `, status, count(*) FROM crimes
		WHERE started_at >= $1 GROUP BY 1, 2`},
	"crimes.categories": {Unit: TInt, SQL: `SELECT ` + day("started_at") + `, category, count(*) FROM crimes
		WHERE started_at >= $1 GROUP BY 1, 2`},
	"jail.sentences": {Unit: TInt, SQL: `SELECT ` + day("starts_at") + `, reason, count(*) FROM jail_sentences
		WHERE starts_at >= $1 GROUP BY 1, 2`},
	"hospital.admissions": {Unit: TInt, SQL: `SELECT ` + day("admitted_at") + `, cause, count(*) FROM hospital_stays
		WHERE admitted_at >= $1 GROUP BY 1, 2`},
	"trade.volume": {Unit: TMoney, SQL: `SELECT ` + day("created_at") + `, 'market', SUM(notional)::bigint FROM market_trades
		WHERE created_at >= $1 GROUP BY 1
		UNION ALL SELECT ` + day("created_at") + `, 'companies', SUM(total)::bigint FROM company_sales
		WHERE created_at >= $1 GROUP BY 1
		UNION ALL SELECT ` + day("created_at") + `, 'shops', SUM(total)::bigint FROM shop_sales
		WHERE created_at >= $1 GROUP BY 1`},
	"travel.departures": {Unit: TInt, SQL: `SELECT ` + day("departed_at") + `, mode, count(*) FROM travels
		WHERE departed_at >= $1 GROUP BY 1, 2`},
	"work.shifts": {Unit: TInt, SQL: `SELECT ` + day("worked_at") + `, 'shifts', count(*) FROM work_shifts
		WHERE worked_at >= $1 GROUP BY 1`},
	"work.wages": {Unit: TMoney, SQL: `SELECT ` + day("worked_at") + `, 'net', SUM(net)::bigint FROM work_shifts
		WHERE worked_at >= $1 GROUP BY 1
		UNION ALL SELECT ` + day("worked_at") + `, 'tax', SUM(tax)::bigint FROM work_shifts WHERE worked_at >= $1 GROUP BY 1`},
	"missions.ended": {Unit: TInt, SQL: `SELECT ` + day("ended_at") + `, status, count(*) FROM mission_assignments
		WHERE ended_at >= $1 GROUP BY 1, 2`},
	"achievements.awarded": {Unit: TInt, SQL: `SELECT ` + day("awarded_at") + `, 'awarded', count(*) FROM player_achievements
		WHERE awarded_at >= $1 GROUP BY 1`},
	"credit.events": {Unit: TInt, SQL: `SELECT ` + day("at") + `, kind, count(*) FROM credit_events
		WHERE at >= $1 GROUP BY 1, 2`},
	"loans.opened": {Unit: TMoney, SQL: `SELECT ` + day("opened_at") + `, product, SUM(principal)::bigint FROM loans
		WHERE opened_at >= $1 GROUP BY 1, 2`},
	"gold.price": {Unit: TMoney, SQL: `SELECT ` + day("set_at") + `, 'price', (AVG(price))::bigint FROM gold_prices
		WHERE set_at >= $1 GROUP BY 1`},
	"gold.volume": {Unit: TInt, SQL: `SELECT ` + day("traded_at") + `, side, SUM(grams)::bigint FROM gold_trades
		WHERE traded_at >= $1 GROUP BY 1, 2`},
	"shares.volume": {Unit: TMoney, SQL: `SELECT ` + day("created_at") + `, 'notional', SUM(notional)::bigint FROM share_trades
		WHERE created_at >= $1 GROUP BY 1`},
	"stock.price": {Unit: TMoney, Scope: "company", SQL: `SELECT ` + day("created_at") + `, 'price',
		(SUM(notional) / NULLIF(SUM(quantity), 0))::bigint FROM share_trades
		WHERE created_at >= $1 AND company_id = $2::uuid GROUP BY 1`},
	"company.books": {Unit: TMoney, Scope: "company", SQL: `SELECT ` + day("started_at") + `, 'revenue', SUM(revenue)::bigint
		FROM company_periods WHERE started_at >= $1 AND company_id = $2::uuid GROUP BY 1
		UNION ALL SELECT ` + day("started_at") + `, 'wages', SUM(wages)::bigint FROM company_periods
		WHERE started_at >= $1 AND company_id = $2::uuid GROUP BY 1
		UNION ALL SELECT ` + day("started_at") + `, 'balance', (array_agg(balance_after ORDER BY period_no DESC))[1]
		FROM company_periods WHERE started_at >= $1 AND company_id = $2::uuid GROUP BY 1`},
	"city.budget": {Unit: TMoney, Scope: "city", SQL: `SELECT ` + day("started_at") + `, 'treasury', (array_agg(treasury ORDER BY period_no DESC))[1]
		FROM city_budget_periods WHERE started_at >= $1 AND city_id = $2::uuid GROUP BY 1
		UNION ALL SELECT ` + day("started_at") + `, 'spent', SUM(spent)::bigint FROM city_budget_periods
		WHERE started_at >= $1 AND city_id = $2::uuid GROUP BY 1`},
	"city.demand": {Unit: TMoney, Scope: "city", SQL: `SELECT ` + day("started_at") + `, 'budget', SUM(budget)::bigint
		FROM company_market_periods WHERE started_at >= $1 AND city_id = $2::uuid GROUP BY 1
		UNION ALL SELECT ` + day("started_at") + `, 'paid', SUM(paid)::bigint FROM company_market_periods
		WHERE started_at >= $1 AND city_id = $2::uuid GROUP BY 1`},
	"military.readiness": {Unit: TBPS, Scope: "country", SQL: `SELECT ` + day("started_at") + `, 'readiness',
		(array_agg(readiness_bps ORDER BY period_no DESC))[1]::bigint FROM military_periods
		WHERE started_at >= $1 AND country_id = $2::uuid GROUP BY 1`},
	"audit.actions": {Unit: TInt, SQL: `SELECT ` + day("created_at") + `, split_part(action, '.', 1), count(*) FROM audit_logs
		WHERE created_at >= $1 GROUP BY 1, 2`},
	"player.cash": {Unit: TMoney, Scope: "player", SQL: `SELECT ` + day("le.created_at") + `, a.kind, SUM(le.amount)::bigint
		FROM ledger_entries le JOIN accounts a ON a.id = le.account_id
		WHERE le.created_at >= $1 AND a.owner_id = $2::uuid GROUP BY 1, 2`},
}

// Series is one chart's data: the days, and one line per key.
type Series struct {
	Name  string       `json:"name"`
	Unit  string       `json:"unit"`
	Days  []string     `json:"days"`
	Lines []SeriesLine `json:"lines"`
}

// SeriesLine is one key's value per day (0 on a day without one).
type SeriesLine struct {
	Key    string  `json:"key"`
	Values []int64 `json:"values"`
	Total  int64   `json:"total"`
}

// dayList is every day from since to now, on the game's clock.
func dayList(since, now time.Time) []string {
	var out []string
	d := since.In(tehran)
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, tehran)
	end := now.In(tehran)
	for t := start; !t.After(end); t = t.AddDate(0, 0, 1) {
		out = append(out, t.Format("2006-01-02"))
	}
	return out
}

// buildSeries lays (day, key, value) rows onto the day list.
func buildSeries(name, unit string, days []string, rows [][]any) Series {
	idx := make(map[string]int, len(days))
	for i, d := range days {
		idx[d] = i
	}
	lines := map[string][]int64{}
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		var dk string
		switch v := r[0].(type) {
		case time.Time:
			dk = v.Format("2006-01-02")
		case string:
			dk = v
		}
		key, _ := r[1].(string)
		val, _ := r[2].(int64)
		i, ok := idx[dk]
		if !ok {
			continue
		}
		if lines[key] == nil {
			lines[key] = make([]int64, len(days))
		}
		lines[key][i] += val
	}
	out := Series{Name: name, Unit: unit, Days: days, Lines: []SeriesLine{}}
	for k, vals := range lines {
		var total int64
		for _, v := range vals {
			total += v
		}
		out.Lines = append(out.Lines, SeriesLine{Key: k, Values: vals, Total: total})
	}
	sort.Slice(out.Lines, func(i, j int) bool {
		if out.Lines[i].Total != out.Lines[j].Total {
			return out.Lines[i].Total > out.Lines[j].Total
		}
		return out.Lines[i].Key < out.Lines[j].Key
	})
	return out
}

// runSeries reads one series over days.
func runSeries(ctx context.Context, r Reader, name string, def SeriesDef, scope string, days int, now time.Time) (Series, error) {
	since := now.In(tehran).AddDate(0, 0, -(days - 1))
	since = time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, tehran)
	args := []any{since.UTC()}
	if def.Scope != "" {
		if scope == "" {
			return Series{}, bad("this series is of one %s", def.Scope)
		}
		ids, err := resolveScopes(ctx, r, map[string]string{def.Scope: scope})
		if err != nil {
			return Series{}, err
		}
		args = append(args, ids[def.Scope])
	}
	t, err := r.Read(ctx, def.SQL, args...)
	if err != nil {
		return Series{}, err
	}
	return buildSeries(name, def.Unit, dayList(since, now), t.Rows), nil
}

// series answers GET /api/series/{name}?days=N[&company=…].
func (s *Server) series(r *http.Request) (any, error) {
	name := r.PathValue("name")
	def, ok := seriesCatalogue[name]
	if !ok {
		return nil, bad("no such series")
	}
	days, err := intQuery(r, "days", 30, 1, 365)
	if err != nil {
		return nil, err
	}
	scope := ""
	if def.Scope != "" {
		scope = r.URL.Query().Get(def.Scope)
	}
	return runSeries(r.Context(), s.reader, name, def, scope, days, s.now())
}
