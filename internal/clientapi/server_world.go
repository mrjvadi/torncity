package clientapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The world and village endpoints (api/client-api.md, "The world").
//
// AUTH. All of them need a signed-in player, like every other endpoint. The
// terrain is public knowledge inside the game, but a chunk is generated on
// demand, so anonymous access would let anyone make a replica compute; the
// per-player rate limit needs a player to count against. The responses are
// still cacheable by anyone (Cache-Control: public), so a CDN in front may
// serve a chunk it has to a request it has not authenticated.

const chunkMediaType = "application/vnd.torncity.chunk"

// chunkCacheControl is what an immutable chunk is served with: its address
// names everything its bytes depend on.
const chunkCacheControl = "public, max-age=31536000, immutable"

var (
	errNoWorldService   = ErrNoWorld
	errNoVillageService = ErrNoSettlement
)

func (s *Server) limited(w http.ResponseWriter, r *http.Request, pr Principal, kind string, perMinute int) bool {
	if perMinute <= 0 || s.cfg.Limits == nil {
		return true
	}
	ok, err := s.cfg.Limits.Allow(r.Context(), kind+":"+pr.PlayerID, perMinute, time.Minute)
	if err != nil {
		s.cfg.Logger.Warn("cannot count requests; letting this one through", "kind", kind, "error", err.Error())
		return true
	}
	if !ok {
		s.fail(w, r, pr.Lang, errRateLimited)
		return false
	}
	return true
}

// worldInfo answers GET /api/v1/world.
func (s *Server) worldInfo(w http.ResponseWriter, r *http.Request, pr Principal) {
	if s.cfg.WorldSvc == nil {
		s.fail(w, r, pr.Lang, errNoWorldService)
		return
	}
	info, err := s.cfg.WorldSvc.Info(r.Context())
	if err != nil {
		s.fail(w, r, pr.Lang, err)
		return
	}
	etag := `"` + info.ID + `.g` + strconv.Itoa(info.GeneratorVersion) + `.` + info.ParamsHash + `"`
	w.Header().Set("ETag", etag)
	// The world never changes once created; a client revalidates rather
	// than trusting a copy, so a replaced world is noticed at once.
	w.Header().Set("Cache-Control", "no-cache")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(info)
}

// chunk answers GET /api/v1/world/chunks/{face}/{lod}/{x}/{y}.
func (s *Server) chunk(w http.ResponseWriter, r *http.Request, pr Principal) {
	if s.cfg.WorldSvc == nil {
		s.fail(w, r, pr.Lang, errNoWorldService)
		return
	}
	if !s.limited(w, r, pr, "chunk", s.cfg.ChunksPerMinute) {
		return
	}
	addr, err := ParseChunkAddr(r.PathValue("face"), r.PathValue("lod"), r.PathValue("x"), r.PathValue("y"))
	if err != nil {
		s.fail(w, r, pr.Lang, err)
		return
	}
	a, err := s.cfg.WorldSvc.Address(r.Context(), addr)
	if err != nil {
		s.fail(w, r, pr.Lang, err)
		return
	}
	gz := acceptsGzip(r.Header.Get("Accept-Encoding"))
	etag := a.ETag
	if gz {
		etag = strings.TrimSuffix(etag, `"`) + `-gz"`
	}
	h := w.Header()
	h.Add("Vary", "Accept-Encoding")
	h.Set("ETag", etag)
	h.Set("Cache-Control", chunkCacheControl)
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	chunk, err := s.cfg.WorldSvc.Chunk(r.Context(), a)
	if err != nil {
		h.Del("ETag")
		h.Del("Cache-Control")
		s.fail(w, r, pr.Lang, err)
		return
	}
	body := chunk.Raw
	if gz {
		body = chunk.Gzip
		h.Set("Content-Encoding", "gzip")
	}
	h.Set("Content-Type", chunkMediaType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// settlementLayout answers GET /api/v1/settlements/{id}/layout.
func (s *Server) settlementLayout(w http.ResponseWriter, r *http.Request, pr Principal) {
	if s.cfg.Villages == nil {
		s.fail(w, r, pr.Lang, errNoVillageService)
		return
	}
	if !s.limited(w, r, pr, "layout", s.cfg.LayoutsPerMinute) {
		return
	}
	layout, err := s.cfg.Villages.Layout(r.Context(), pr.PlayerID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, pr.Lang, err)
		return
	}
	etag := layout.ETag()
	h := w.Header()
	h.Set("ETag", etag)
	// Private to the asker (a member sees more than a stranger) and always
	// revalidated: the version is cheap to compare, the layout is not
	// cheap to be wrong about.
	h.Set("Cache-Control", "private, no-cache")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(layout)
}
