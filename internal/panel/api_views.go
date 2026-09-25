package panel

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// Reader runs the panel's read-only statements (postgres.EconomyAdmin
// PanelRead over the pool, with the configured timeout).
type Reader interface {
	Read(ctx context.Context, sql string, args ...any) (postgres.PanelTable, error)
}

// maxOffset bounds how deep a list may be paged.
const maxOffset = 100_000

// parseQuery reads a view's request: scopes by name, f_<key> filters, q,
// from/to (dates or instants), sort/dir, limit/offset.
func parseQuery(v View, vals url.Values, maxLimit int) (Query, map[string]string, error) {
	q := Query{Filters: map[string]string{}, Limit: 50}
	raw := map[string]string{}
	for name := range v.Scopes {
		if s := strings.TrimSpace(vals.Get(name)); s != "" {
			raw[name] = s
		}
	}
	for k, list := range vals {
		if !strings.HasPrefix(k, "f_") || len(list) == 0 || strings.TrimSpace(list[0]) == "" {
			continue
		}
		key := strings.TrimPrefix(k, "f_")
		val := strings.TrimSpace(list[0])
		if !identifier.MatchString(key) || len(val) > 80 {
			return q, nil, bad("filter %s is not valid", key)
		}
		q.Filters[key] = val
	}
	q.Search = strings.TrimSpace(vals.Get("q"))
	if len([]rune(q.Search)) > 64 {
		return q, nil, bad("the search is too long")
	}
	for _, p := range []struct {
		name string
		into **time.Time
	}{{"from", &q.From}, {"to", &q.To}} {
		if s := vals.Get(p.name); s != "" {
			t, err := parseInstant(s)
			if err != nil {
				return q, nil, bad("%s is not a date", p.name)
			}
			*p.into = &t
		}
	}
	q.Sort = vals.Get("sort")
	if q.Sort != "" && !identifier.MatchString(q.Sort) {
		return q, nil, bad("sort is not a column")
	}
	q.Desc = vals.Get("dir") != "asc"
	if s := vals.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxLimit {
			return q, nil, bad("limit must be a whole number from 1 to %d", maxLimit)
		}
		q.Limit = n
	}
	if s := vals.Get("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > maxOffset {
			return q, nil, bad("offset must be a whole number from 0 to %d", maxOffset)
		}
		q.Offset = n
	}
	return q, raw, nil
}

// parseInstant reads 2026-09-25 (Tehran midnight) or an RFC 3339 instant.
func parseInstant(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.ParseInLocation("2006-01-02", s, tehran)
}

var tehran = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		return time.FixedZone("IRST", 3*3600+1800)
	}
	return loc
}()

// resolveScopes turns each scope's value into the id its predicate
// compares. An unknown thing is a 404.
func resolveScopes(ctx context.Context, r Reader, raw map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(raw))
	for name, val := range raw {
		kind, ok := scopeKinds[name]
		if !ok {
			return nil, bad("unknown scope %s", name)
		}
		if kind.norm != nil {
			val = kind.norm(val)
		}
		if kind.valid != nil && !kind.valid.MatchString(val) {
			return nil, bad("%s is not a valid %s", val, name)
		}
		if kind.resolve == "" {
			out[name] = val
			continue
		}
		t, err := r.Read(ctx, kind.resolve, val)
		if err != nil {
			return nil, err
		}
		if len(t.Rows) == 0 || len(t.Rows[0]) == 0 {
			return nil, fmt.Errorf("%w: %s %s", postgres.ErrNotFound, name, val)
		}
		id, _ := t.Rows[0][0].(string)
		out[name] = id
	}
	return out, nil
}

// runView reads one page of a view.
func runView(ctx context.Context, r Reader, v View, q Query, raw map[string]string, now time.Time) (ViewPage, error) {
	page := ViewPage{ViewMeta: v.meta(), Offset: q.Offset, Limit: q.Limit, Rows: [][]any{}}
	scopes, err := resolveScopes(ctx, r, raw)
	if err != nil {
		return page, err
	}
	q.Scopes = scopes
	want := q.Limit
	q.Limit++
	sql, args, err := v.statement(q, now)
	if err != nil {
		return page, err
	}
	t, err := r.Read(ctx, sql, args...)
	if err != nil {
		return page, err
	}
	rows := t.Rows
	if len(rows) > want {
		rows, page.More = rows[:want], true
	}
	if rows != nil {
		page.Rows = rows
	}
	return page, nil
}

// views answers GET /api/views: the catalogue's metadata.
func (s *Server) views(*http.Request) (any, error) {
	out := make([]ViewMeta, 0, len(catalogue))
	for _, n := range ViewNames() {
		out = append(out, catalogue[n].meta())
	}
	return out, nil
}

// view answers GET /api/views/{name}, or its CSV export with format=csv.
func (s *Server) view(w http.ResponseWriter, r *http.Request) {
	if s.reader == nil {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	v, ok := catalogue[r.PathValue("name")]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	csvOut := r.URL.Query().Get("format") == "csv"
	limit := 200
	if csvOut {
		limit = s.cfg.ExportMaxRows
	}
	q, raw, err := parseQuery(v, r.URL.Query(), limit)
	if err == nil && csvOut && r.URL.Query().Get("limit") == "" {
		q.Limit = s.cfg.ExportMaxRows
	}
	var page ViewPage
	if err == nil {
		page, err = runView(r.Context(), s.reader, v, q, raw, s.now())
	}
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	if !csvOut {
		writeJSON(w, http.StatusOK, page)
		return
	}
	s.auditExport(r, v.Name, len(page.Rows))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.csv"`,
		strings.ReplaceAll(v.Name, ".", "-"), s.now().UTC().Format("20060102-1504")))
	_, _ = w.Write([]byte("\xef\xbb\xbf"))
	cw := csv.NewWriter(w)
	head := make([]string, 0, len(v.Cols))
	for _, c := range v.Cols {
		head = append(head, c.Key)
	}
	_ = cw.Write(head)
	for _, row := range page.Rows {
		rec := make([]string, len(row))
		for i, val := range row {
			rec[i] = csvCell(val)
		}
		_ = cw.Write(rec)
	}
	cw.Flush()
}

// csvCell writes one value; a cell that a spreadsheet would read as a
// formula is quoted with a leading apostrophe.
func csvCell(v any) string {
	var s string
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		s = x
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		b, _ := json.Marshal(x)
		s = string(b)
	}
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			s = "'" + s
		}
	}
	return s
}

// auditExport records that an operator took a list out of the panel.
func (s *Server) auditExport(r *http.Request, view string, rows int) {
	sess := sessionOf(r)
	if err := s.accounts.AuditPanel(r.Context(), "panel:"+sess.Username, "panel.export", "export",
		map[string]any{"view": view, "rows": rows, "query": r.URL.RawQuery}, s.now()); err != nil {
		s.log.Error("panel: writing an export audit row", "error", err.Error())
	}
}

// readFailed maps a read's error to its answer, as read does.
func (s *Server) readFailed(w http.ResponseWriter, r *http.Request, err error) {
	var b badRequest
	if errors.As(err, &b) {
		writeError(w, http.StatusBadRequest, "bad_request", b.msg)
		return
	}
	status := statusOf(err, http.StatusInternalServerError)
	if status == http.StatusInternalServerError {
		s.log.Error("panel: a read failed", "path", r.URL.Path, "error", err.Error())
		writeError(w, status, "internal", "")
		return
	}
	writeError(w, status, codeOf(err, "failed"), "")
}
