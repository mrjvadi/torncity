package panel

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// THE CATALOGUE OF LISTS.
//
// Almost everything the panel shows is a list: a player's ledger entries, a
// company's periods, a country's garrisons, the jail. Each is one View — a
// constant statement whose output columns are declared, with the scopes
// (whose list: a player's, a company's…), filters, search, time range and
// sorts it accepts. One endpoint serves them all (GET /api/views/{name}),
// paged, sortable and exportable, and the web client renders them with one
// table. A statement never has operator input spliced into it: the column
// names come from the catalogue and every value is bound as a parameter.
//
// A statement is wrapped: SELECT <declared columns> FROM (<statement>) v
// WHERE <scope, filters, search, range> ORDER BY … LIMIT … OFFSET …. The
// planner flattens the wrapper into the statement, so a scope's predicate
// still reaches the table's index. A statement that aggregates marks where
// the time range belongs with /*window*/, which is replaced by the range's
// predicate on the view's Window expression, inside the aggregate.

// Column types the client knows how to show.
const (
	TText    = "text"
	TCode    = "code"
	TInt     = "int"
	TMoney   = "money"
	TBPS     = "bps"
	TTime    = "time"
	TBool    = "bool"
	TStatus  = "status"
	TJSON    = "json"
	TPlayer  = "player"
	TCompany = "company"
	TCity    = "city"
	TCountry = "country"
	TFaction = "faction"
	TItem    = "item"
	TID      = "id"
	TSeconds = "seconds"
	TList    = "list"
	// TRef is "kind:code" — a player, company, city, country or faction —
	// for a column whose rows name different kinds of thing.
	TRef = "ref"
	// TEnum is a code shown in words where the client has some.
	TEnum = "enum"
)

// Col is one column a view shows.
type Col struct {
	Key  string `json:"key"`
	Type string `json:"type"`
	Sort bool   `json:"sort"`
}

// Filter is an equality filter a view accepts: one of Options when they are
// given, else any code-like value. Prefix filters match the start.
type Filter struct {
	Key     string   `json:"key"`
	Options []string `json:"options,omitempty"`
	Prefix  bool     `json:"prefix,omitempty"`
}

// View is one list.
type View struct {
	Name string
	SQL  string
	Cols []Col
	// Scopes maps a scope (see scopeKinds) to its predicate over the
	// statement's columns (as v.column), with @ where the scope's resolved
	// value goes.
	Scopes map[string]string
	// Need names scopes of which one is required: a list too large to show
	// whole (every ledger entry of every account) is only ever shown scoped
	// or filtered.
	Need    []string
	Filters []Filter
	Search  []string
	// Order is the default sort column; Asc its direction.
	Order string
	Asc   bool
	// Tie is a column (shown or not) that makes the order total.
	Tie string
	// Time is the column the from/to range applies to, outside the
	// statement; Window the expression it applies to inside it, for a
	// statement marked /*window*/. WindowDays is the range an aggregate
	// takes when none is given.
	Time       string
	Window     string
	WindowDays int
}

// cols reads "key:type" specs; a trailing "-" makes a column unsortable.
func cols(specs ...string) []Col {
	out := make([]Col, 0, len(specs))
	for _, s := range specs {
		key, typ, _ := strings.Cut(s, ":")
		sortable := !strings.HasSuffix(typ, "-")
		typ = strings.TrimSuffix(typ, "-")
		if typ == TJSON || typ == TList {
			sortable = false
		}
		out = append(out, Col{Key: key, Type: typ, Sort: sortable})
	}
	return out
}

func opts(key string, options ...string) Filter { return Filter{Key: key, Options: options} }
func free(key string) Filter                    { return Filter{Key: key} }

var identifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// catalogue is every view by name.
var catalogue = map[string]View{}

func register(views ...View) {
	for _, v := range views {
		if _, dup := catalogue[v.Name]; dup {
			panic("panel: two views named " + v.Name)
		}
		catalogue[v.Name] = v
	}
}

// ViewNames lists the catalogue, sorted.
func ViewNames() []string {
	out := make([]string, 0, len(catalogue))
	for n := range catalogue {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// check reports what is wrong with a view's declaration, for the test that
// guards the catalogue.
func (v View) check() error {
	if !regexp.MustCompile(`^[a-z][a-z0-9_.]*$`).MatchString(v.Name) {
		return fmt.Errorf("view name %q", v.Name)
	}
	known := map[string]bool{}
	for _, c := range v.Cols {
		if !identifier.MatchString(c.Key) {
			return fmt.Errorf("%s: column %q", v.Name, c.Key)
		}
		known[c.Key] = true
	}
	for _, f := range v.Filters {
		if !identifier.MatchString(f.Key) {
			return fmt.Errorf("%s: filter %q", v.Name, f.Key)
		}
	}
	for _, k := range append([]string{v.Order, v.Tie, v.Time}, v.Search...) {
		if k != "" && !identifier.MatchString(k) {
			return fmt.Errorf("%s: key %q", v.Name, k)
		}
	}
	if v.Order != "" && !known[v.Order] {
		return fmt.Errorf("%s: order %q is not a shown column", v.Name, v.Order)
	}
	for s, pred := range v.Scopes {
		if _, ok := scopeKinds[s]; !ok {
			return fmt.Errorf("%s: unknown scope %q", v.Name, s)
		}
		if !strings.Contains(pred, "@") {
			return fmt.Errorf("%s: scope %q has no @", v.Name, s)
		}
	}
	for _, s := range v.Need {
		if _, ok := v.Scopes[s]; !ok {
			return fmt.Errorf("%s: needs unknown scope %q", v.Name, s)
		}
	}
	if strings.Contains(v.SQL, "/*window*/") != (v.Window != "") {
		return fmt.Errorf("%s: a window marker and a window expression go together", v.Name)
	}
	return nil
}

// Query is one request of a view, its scopes already resolved.
type Query struct {
	Scopes  map[string]string
	Filters map[string]string
	Search  string
	From    *time.Time
	To      *time.Time
	Sort    string
	Desc    bool
	Limit   int
	Offset  int
}

// statement builds the SQL and its arguments for a query.
func (v View) statement(q Query, now time.Time) (string, []any, error) {
	var args []any
	bind := func(val any) string {
		args = append(args, val)
		return "$" + strconv.Itoa(len(args))
	}
	base := v.SQL
	if v.Window != "" {
		from, to := q.From, q.To
		if from == nil {
			days := v.WindowDays
			if days <= 0 {
				days = 30
			}
			f := now.Add(-time.Duration(days) * 24 * time.Hour)
			from = &f
		}
		pred := " AND " + v.Window + " >= " + bind(from.UTC())
		if to != nil {
			pred += " AND " + v.Window + " < " + bind(to.UTC())
		}
		base = strings.ReplaceAll(base, "/*window*/", pred)
	}
	var where []string
	scopes := make([]string, 0, len(q.Scopes))
	for s := range q.Scopes {
		scopes = append(scopes, s)
	}
	sort.Strings(scopes)
	for _, s := range scopes {
		pred, ok := v.Scopes[s]
		if !ok {
			return "", nil, bad("this list cannot be scoped by %s", s)
		}
		ph := bind(q.Scopes[s])
		where = append(where, "("+strings.ReplaceAll(pred, "@", ph)+")")
	}
	if len(v.Need) > 0 {
		found := false
		for _, s := range v.Need {
			if _, ok := q.Scopes[s]; ok {
				found = true
			}
		}
		if !found && len(q.Filters) == 0 {
			return "", nil, bad("this list is shown for one %s at a time", strings.Join(v.Need, ", "))
		}
	}
	filters := make([]string, 0, len(q.Filters))
	for k := range q.Filters {
		filters = append(filters, k)
	}
	sort.Strings(filters)
	for _, k := range filters {
		f, ok := v.filter(k)
		if !ok {
			return "", nil, bad("this list has no filter %s", k)
		}
		val := q.Filters[k]
		if len(f.Options) > 0 && !contains(f.Options, val) {
			return "", nil, bad("%s cannot be %q", k, val)
		}
		if f.Prefix {
			where = append(where, "starts_with(v."+k+"::text, "+bind(val)+")")
		} else {
			where = append(where, "v."+k+"::text = "+bind(val))
		}
	}
	if s := strings.TrimSpace(q.Search); s != "" && len(v.Search) > 0 {
		ph := bind("%" + likeEscape(s) + "%")
		var ors []string
		for _, k := range v.Search {
			ors = append(ors, "v."+k+"::text ILIKE "+ph)
		}
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}
	if v.Time != "" && v.Window == "" {
		if q.From != nil {
			where = append(where, "v."+v.Time+" >= "+bind(q.From.UTC()))
		}
		if q.To != nil {
			where = append(where, "v."+v.Time+" < "+bind(q.To.UTC()))
		}
	}
	order := v.Order
	desc := !v.Asc
	if q.Sort != "" {
		c, ok := v.col(q.Sort)
		if !ok || !c.Sort {
			return "", nil, bad("this list cannot be sorted by %s", q.Sort)
		}
		order, desc = q.Sort, q.Desc
	}
	names := make([]string, 0, len(v.Cols))
	for _, c := range v.Cols {
		names = append(names, "v."+c.Key)
	}
	var b strings.Builder
	b.WriteString("SELECT " + strings.Join(names, ", ") + " FROM (\n" + base + "\n) v")
	if len(where) > 0 {
		b.WriteString(" WHERE " + strings.Join(where, " AND "))
	}
	if order != "" {
		dir := " ASC NULLS LAST"
		if desc {
			dir = " DESC NULLS LAST"
		}
		b.WriteString(" ORDER BY v." + order + dir)
		if v.Tie != "" && v.Tie != order {
			b.WriteString(", v." + v.Tie + map[bool]string{true: " DESC", false: " ASC"}[desc])
		}
	}
	b.WriteString(" LIMIT " + bind(q.Limit) + " OFFSET " + bind(q.Offset))
	return b.String(), args, nil
}

func (v View) filter(k string) (Filter, bool) {
	for _, f := range v.Filters {
		if f.Key == k {
			return f, true
		}
	}
	return Filter{}, false
}

func (v View) col(k string) (Col, bool) {
	for _, c := range v.Cols {
		if c.Key == k {
			return c, true
		}
	}
	return Col{}, false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
}

// ViewMeta is what the client learns of a view with its rows.
type ViewMeta struct {
	Name    string   `json:"name"`
	Columns []Col    `json:"columns"`
	Filters []Filter `json:"filters"`
	Scopes  []string `json:"scopes"`
	Search  bool     `json:"search"`
	Time    bool     `json:"time"`
	Order   string   `json:"order"`
	Desc    bool     `json:"desc"`
}

func (v View) meta() ViewMeta {
	m := ViewMeta{Name: v.Name, Columns: v.Cols, Filters: v.Filters, Search: len(v.Search) > 0,
		Time: v.Time != "" || v.Window != "", Order: v.Order, Desc: !v.Asc}
	if m.Filters == nil {
		m.Filters = []Filter{}
	}
	for s := range v.Scopes {
		m.Scopes = append(m.Scopes, s)
	}
	sort.Strings(m.Scopes)
	if m.Scopes == nil {
		m.Scopes = []string{}
	}
	return m
}

// ViewPage is one page of a view.
type ViewPage struct {
	ViewMeta
	Rows   [][]any `json:"rows"`
	More   bool    `json:"more"`
	Offset int     `json:"offset"`
	Limit  int     `json:"limit"`
}
