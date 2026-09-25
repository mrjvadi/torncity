package panel

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"time"
)

// Entry points to the catalogue for the integration suite (tests/), which
// runs every view and series against the real schema.

// CheckView reports what is wrong with a view's declaration.
func CheckView(name string) error {
	v, ok := catalogue[name]
	if !ok {
		return fmt.Errorf("no view %q", name)
	}
	return v.check()
}

// RunView reads one page of a view for a query string.
func RunView(ctx context.Context, r Reader, name string, query url.Values, now time.Time) (ViewPage, error) {
	v, ok := catalogue[name]
	if !ok {
		return ViewPage{}, fmt.Errorf("no view %q", name)
	}
	q, raw, err := parseQuery(v, query, 200)
	if err != nil {
		return ViewPage{}, err
	}
	return runView(ctx, r, v, q, raw, now)
}

// sampleValues are values of each scope that the sample queries use; a
// scope resolved to nothing answers not found, which the suite accepts.
var sampleValues = map[string]string{
	"player": "ZZZZZZZ", "company": "ZZZZZZ", "city": "ostmarch", "here": "ostmarch", "country": "default_country", "faction": "ZZZZZZ",
	"war": "1", "election": "1", "proposal": "1", "loan": "1", "policy": "1", "auction": "1", "operation": "1",
	"crew": "1", "property": "1", "design": "1", "dividend": "1", "piece": "none",
	"tx": "00000000-0000-4000-8000-000000000009", "account": "00000000-0000-4000-8000-000000000001",
	"action": "00000000-0000-4000-8000-000000000009", "reference": "00000000-0000-4000-8000-000000000009",
	"item": "bread", "jurisdiction": "country:default_country",
}

// SampleQueries are the queries the suite runs for a view: plain (or with
// its first scope when it needs one), each scope, each filter, a search, a
// time range, and every sortable column.
func SampleQueries(name, player string) []url.Values {
	v := catalogue[name]
	values := map[string]string{"player": player}
	for k, s := range sampleValues {
		values[k] = s
	}
	var scopes []string
	for s := range v.Scopes {
		scopes = append(scopes, s)
	}
	sort.Strings(scopes)
	base := url.Values{}
	if len(v.Need) > 0 {
		base.Set(v.Need[0], values[v.Need[0]])
		if v.Need[0] == "player" {
			base.Set("player", player)
		}
	}
	var out []url.Values
	with := func(k, val string) url.Values {
		q := url.Values{}
		for bk, bv := range base {
			q[bk] = bv
		}
		if k != "" {
			q.Set(k, val)
		}
		return q
	}
	out = append(out, with("", ""))
	for _, s := range scopes {
		out = append(out, with(s, values[s]))
	}
	for _, f := range v.Filters {
		val := "x"
		if len(f.Options) > 0 {
			val = f.Options[0]
		}
		out = append(out, with("f_"+f.Key, val))
	}
	if len(v.Search) > 0 {
		out = append(out, with("q", "a"))
	}
	if v.Time != "" || v.Window != "" {
		q := with("from", "2020-01-01")
		q.Set("to", "2100-01-01")
		out = append(out, q)
	}
	for _, c := range v.Cols {
		if c.Sort {
			q := with("sort", c.Key)
			q.Set("dir", "asc")
			out = append(out, q)
		}
	}
	return out
}

// SeriesNames lists the series catalogue.
func SeriesNames() []string {
	out := make([]string, 0, len(seriesCatalogue))
	for n := range seriesCatalogue {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// RunSeries reads one series; a scoped series with no scope is a bad
// request.
func RunSeries(ctx context.Context, r Reader, name, scope string, days int, now time.Time) (Series, error) {
	def, ok := seriesCatalogue[name]
	if !ok {
		return Series{}, fmt.Errorf("no series %q", name)
	}
	if def.Scope != "" && scope == "" {
		scope = sampleValues[def.Scope]
	}
	return runSeries(ctx, r, name, def, scope, days, now)
}

// IsBadRequest reports whether err is a request's fault.
func IsBadRequest(err error) bool {
	var b badRequest
	return errors.As(err, &b)
}
