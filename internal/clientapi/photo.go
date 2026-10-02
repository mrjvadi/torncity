package clientapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GET /api/me/photo: the player's Telegram profile photo, fetched through the
// Bot API and proxied, so the client can draw it without Telegram's own URL
// (which carries the bot token) ever reaching a browser.
//
// The Bot API (https://core.telegram.org/bots/api):
//   - getUserProfilePhotos(user_id, limit) answers UserProfilePhotos
//     {total_count, photos: [[PhotoSize]]}: each photo as several sizes,
//     smallest first;
//   - getFile(file_id) answers a File with file_path;
//   - the bytes are downloaded from https://api.telegram.org/file/bot<token>/<file_path>
//     (the link is valid for at least an hour, files up to 20 MB).
//
// One fetch is three calls, so the result is cached (Redis, config
// client.photo_ttl), and so is the absence of a photo (client.photo_missing_ttl),
// and the fetches one player may cause are rate limited. The token appears
// only in the request to Telegram; errors are rebuilt without the URL so it
// is never logged or answered.

// ErrNoPhoto means the player has no profile photo (or no bot can see it).
var ErrNoPhoto = errors.New("clientapi: no profile photo")

// PhotoStore is the cache (infraredis.BlobCache).
type PhotoStore interface {
	// Get returns the value under key; found is false when there is none.
	Get(ctx context.Context, key string) (value []byte, found bool, err error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}

// PhotoService fetches and caches profile photos.
type PhotoService struct {
	// Bots are the bots to ask, the player's own first.
	Bots  func(ctx context.Context) ([]BotCredential, error)
	Store PhotoStore
	HTTP  *http.Client
	// API is the Bot API's base address; empty is https://api.telegram.org.
	API string
	// TTL and MissingTTL are how long a photo and "no photo" are kept.
	TTL, MissingTTL time.Duration
	// MaxBytes bounds a downloaded photo (a profile photo is a few dozen KB).
	MaxBytes int64

	mu     sync.Mutex
	flight map[string]*photoCall
}

type photoCall struct {
	done chan struct{}
	data []byte
	err  error
}

// maxPhotoBytes is the default bound on a downloaded photo.
const maxPhotoBytes = 2 << 20

// missingMark is what the cache holds for "this player has no photo".
var missingMark = []byte{'-'}

func (p *PhotoService) api() string {
	if p.API != "" {
		return strings.TrimRight(p.API, "/")
	}
	return "https://api.telegram.org"
}

// Photo returns the player's profile photo bytes, or ErrNoPhoto. rate, when
// set, is asked before any Bot API call (a cache hit costs nothing).
func (p *PhotoService) Photo(ctx context.Context, telegramUserID int64, botID string, rate func() bool) ([]byte, error) {
	key := "photo:" + strconv.FormatInt(telegramUserID, 10)
	if v, found, err := p.Store.Get(ctx, key); err == nil && found {
		if bytes.Equal(v, missingMark) {
			return nil, ErrNoPhoto
		}
		return v, nil
	}
	if rate != nil && !rate() {
		return nil, errRateLimited
	}

	// One fetch per user at a time on this replica; other replicas race
	// harmlessly (they write the same bytes).
	p.mu.Lock()
	if p.flight == nil {
		p.flight = map[string]*photoCall{}
	}
	if c, ok := p.flight[key]; ok {
		p.mu.Unlock()
		select {
		case <-c.done:
			return c.data, c.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	c := &photoCall{done: make(chan struct{})}
	p.flight[key] = c
	p.mu.Unlock()

	// A fetch outlives one caller's cancellation so the others get its result.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	c.data, c.err = p.fetch(fctx, telegramUserID, botID)
	switch {
	case c.err == nil:
		_ = p.Store.Set(fctx, key, c.data, p.TTL)
	case errors.Is(c.err, ErrNoPhoto):
		_ = p.Store.Set(fctx, key, missingMark, p.MissingTTL)
	}
	p.mu.Lock()
	delete(p.flight, key)
	p.mu.Unlock()
	close(c.done)
	return c.data, c.err
}

// fetch asks the player's own bot first, then the others, and stops at the
// first photo. Only when every bot answered "none" is it ErrNoPhoto; a bot
// that failed makes the whole answer a failure (nothing is cached).
func (p *PhotoService) fetch(ctx context.Context, userID int64, botID string) ([]byte, error) {
	bots, err := p.Bots(ctx)
	if err != nil {
		return nil, fmt.Errorf("clientapi: reading the bots: %w", err)
	}
	ordered := make([]BotCredential, 0, len(bots))
	for _, b := range bots {
		if b.ID == botID {
			ordered = append(ordered, b)
		}
	}
	for _, b := range bots {
		if b.ID != botID {
			ordered = append(ordered, b)
		}
	}
	var failure error
	for _, b := range ordered {
		data, err := p.fetchWith(ctx, b.Token, userID)
		switch {
		case err == nil:
			return data, nil
		case errors.Is(err, ErrNoPhoto):
		default:
			failure = err
		}
	}
	if failure != nil {
		return nil, failure
	}
	return nil, ErrNoPhoto
}

type botReply struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

// call runs one Bot API method. The returned error never carries the URL.
func (p *PhotoService) call(ctx context.Context, token, method string, args url.Values) (json.RawMessage, int, error) {
	u := p.api() + "/bot" + token + "/" + method + "?" + args.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, errors.New("clientapi: cannot build a Bot API request")
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("clientapi: the Bot API %s failed: %s", method, scrub(err))
	}
	defer resp.Body.Close()
	var out botReply
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("clientapi: the Bot API %s answered garbage (HTTP %d)", method, resp.StatusCode)
	}
	if !out.OK {
		return nil, resp.StatusCode, fmt.Errorf("clientapi: the Bot API %s refused (HTTP %d): %s", method, resp.StatusCode, out.Description)
	}
	return out.Result, resp.StatusCode, nil
}

// scrub is an error's text without any URL in it.
func scrub(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}

func (p *PhotoService) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// fetchWith runs the three calls with one bot's token.
func (p *PhotoService) fetchWith(ctx context.Context, token string, userID int64) ([]byte, error) {
	raw, status, err := p.call(ctx, token, "getUserProfilePhotos",
		url.Values{"user_id": {strconv.FormatInt(userID, 10)}, "limit": {"1"}})
	if err != nil {
		// A bot that has never met the user answers 400: no photo from it.
		if status == http.StatusBadRequest || status == http.StatusForbidden {
			return nil, ErrNoPhoto
		}
		return nil, err
	}
	var photos struct {
		Total  int `json:"total_count"`
		Photos [][]struct {
			FileID string `json:"file_id"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
			Size   int64  `json:"file_size"`
		} `json:"photos"`
	}
	if err := json.Unmarshal(raw, &photos); err != nil {
		return nil, errors.New("clientapi: the Bot API's profile photos could not be read")
	}
	if photos.Total == 0 || len(photos.Photos) == 0 || len(photos.Photos[0]) == 0 {
		return nil, ErrNoPhoto
	}
	// The smallest size that is still sharp on a retina avatar (at least
	// 160 px); else the largest there is.
	sizes := photos.Photos[0]
	pick := sizes[len(sizes)-1].FileID
	for _, s := range sizes {
		if s.Width >= 160 {
			pick = s.FileID
			break
		}
	}
	raw, _, err = p.call(ctx, token, "getFile", url.Values{"file_id": {pick}})
	if err != nil {
		return nil, err
	}
	var file struct {
		Path string `json:"file_path"`
	}
	if err := json.Unmarshal(raw, &file); err != nil || file.Path == "" {
		return nil, errors.New("clientapi: the Bot API gave no file path")
	}
	if strings.Contains(file.Path, "..") {
		return nil, errors.New("clientapi: the Bot API gave a strange file path")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.api()+"/file/bot"+token+"/"+strings.TrimLeft(file.Path, "/"), nil)
	if err != nil {
		return nil, errors.New("clientapi: cannot build a Bot API download")
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("clientapi: the photo download failed: %s", scrub(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("clientapi: the photo download answered HTTP %d", resp.StatusCode)
	}
	limit := p.MaxBytes
	if limit <= 0 {
		limit = maxPhotoBytes
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("clientapi: the photo download broke: %s", scrub(err))
	}
	if int64(len(data)) > limit || len(data) < 2 {
		return nil, errors.New("clientapi: the photo is not a usable size")
	}
	return data, nil
}

// mePhoto answers GET /api/me/photo: the bytes with caching headers, or 404
// when there is no photo (the client then shows initials).
func (s *Server) mePhoto(w http.ResponseWriter, r *http.Request, pr Principal) {
	if s.cfg.Photos == nil || pr.TelegramUserID == 0 {
		http.NotFound(w, r)
		return
	}
	rate := func() bool {
		if s.cfg.PhotosPerMinute <= 0 {
			return true
		}
		ok, err := s.cfg.Limits.Allow(r.Context(), "photo:"+pr.PlayerID, s.cfg.PhotosPerMinute, time.Minute)
		if err != nil {
			s.cfg.Logger.Warn("cannot count photo fetches; letting this one through", "error", err.Error())
			return true
		}
		return ok
	}
	data, err := s.cfg.Photos.Photo(r.Context(), pr.TelegramUserID, pr.BotID, rate)
	switch {
	case errors.Is(err, ErrNoPhoto):
		w.Header().Set("Cache-Control", "private, max-age=300")
		http.NotFound(w, r)
		return
	case errors.Is(err, errRateLimited):
		s.fail(w, r, pr.Lang, err)
		return
	case err != nil:
		// Telegram is unreachable or refused: not "no photo", try again later.
		s.cfg.Logger.Warn("cannot fetch a profile photo", "error", err.Error())
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "photo unavailable", http.StatusBadGateway)
		return
	}
	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	h := w.Header()
	h.Set("Content-Type", http.DetectContentType(data))
	h.Set("Cache-Control", "private, max-age="+strconv.Itoa(int(s.cfg.PhotoMaxAge/time.Second)))
	h.Set("ETag", etag)
	h.Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}
