package clientapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type memStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (s *memStore) Get(_ context.Context, k string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	return v, ok, nil
}

func (s *memStore) Set(_ context.Context, k string, v []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string][]byte{}
	}
	s.m[k] = v
	return nil
}

// fakeBotAPI answers the three calls for one token.
func fakeBotAPI(t *testing.T, token string, photos bool, calls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		switch {
		case r.URL.Path == "/bot"+token+"/getUserProfilePhotos":
			if !photos {
				_, _ = w.Write([]byte(`{"ok":true,"result":{"total_count":0,"photos":[]}}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{"total_count":1,"photos":[[{"file_id":"s","width":80,"height":80},{"file_id":"m","width":160,"height":160}]]}}`))
		case r.URL.Path == "/bot"+token+"/getFile":
			if r.URL.Query().Get("file_id") != "m" {
				t.Errorf("picked %q, want the 160px size", r.URL.Query().Get("file_id"))
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_path":"photos/a.jpg"}}`))
		case r.URL.Path == "/file/bot"+token+"/photos/a.jpg":
			_, _ = w.Write([]byte("\xff\xd8\xff\xe0jpegbytes"))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestPhotoFetchedOnceThenCached(t *testing.T) {
	calls := 0
	srv := fakeBotAPI(t, "T0KEN", true, &calls)
	defer srv.Close()
	p := &PhotoService{Bots: func(context.Context) ([]BotCredential, error) { return []BotCredential{{ID: "b", Token: "T0KEN"}}, nil },
		Store: &memStore{}, API: srv.URL, TTL: time.Hour, MissingTTL: time.Minute}
	for i := 0; i < 2; i++ {
		data, err := p.Photo(context.Background(), 42, "b", nil)
		if err != nil || !strings.Contains(string(data), "jpegbytes") {
			t.Fatalf("photo = %q, %v", data, err)
		}
	}
	if calls != 3 {
		t.Errorf("Bot API calls = %d, want 3 (the second read is cached)", calls)
	}
}

func TestNoPhotoIsRememberedAndIs404(t *testing.T) {
	calls := 0
	srv := fakeBotAPI(t, "T0KEN", false, &calls)
	defer srv.Close()
	store := &memStore{}
	p := &PhotoService{Bots: func(context.Context) ([]BotCredential, error) { return []BotCredential{{ID: "b", Token: "T0KEN"}}, nil },
		Store: store, API: srv.URL, TTL: time.Hour, MissingTTL: time.Minute}
	for i := 0; i < 2; i++ {
		if _, err := p.Photo(context.Background(), 42, "b", nil); !errors.Is(err, ErrNoPhoto) {
			t.Fatalf("err = %v, want ErrNoPhoto", err)
		}
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (absence is cached)", calls)
	}
	s := &Server{cfg: ServerConfig{Photos: p, PhotoMaxAge: time.Hour, Limits: allowAll{}}}
	rec := httptest.NewRecorder()
	s.mePhoto(rec, httptest.NewRequest(http.MethodGet, "/api/me/photo", nil), Principal{PlayerID: "p", TelegramUserID: 42, BotID: "b"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

type allowAll struct{}

func (allowAll) Allow(context.Context, string, int, time.Duration) (bool, error) { return true, nil }

func TestPhotoHeadersAndTokenNeverLeaks(t *testing.T) {
	calls := 0
	srv := fakeBotAPI(t, "T0KEN", true, &calls)
	p := &PhotoService{Bots: func(context.Context) ([]BotCredential, error) { return []BotCredential{{ID: "b", Token: "T0KEN"}}, nil },
		Store: &memStore{}, API: srv.URL, TTL: time.Hour, MissingTTL: time.Minute}
	s := &Server{cfg: ServerConfig{Photos: p, PhotoMaxAge: time.Hour, Limits: allowAll{}}}
	rec := httptest.NewRecorder()
	pr := Principal{PlayerID: "p", TelegramUserID: 42, BotID: "b"}
	s.mePhoto(rec, httptest.NewRequest(http.MethodGet, "/api/me/photo", nil), pr)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || !strings.Contains(rec.Header().Get("Cache-Control"), "max-age=3600") {
		t.Fatalf("status %d headers %v", rec.Code, rec.Header())
	}
	etag := rec.Header().Get("ETag")
	req := httptest.NewRequest(http.MethodGet, "/api/me/photo", nil)
	req.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	s.mePhoto(rec2, req, pr)
	if rec2.Code != http.StatusNotModified {
		t.Errorf("revalidation = %d, want 304", rec2.Code)
	}

	// Telegram unreachable: the error text must not carry the token.
	srv.Close()
	p2 := &PhotoService{Bots: p.Bots, Store: &memStore{}, API: srv.URL, TTL: time.Hour, MissingTTL: time.Minute}
	_, err := p2.Photo(context.Background(), 7, "b", nil)
	if err == nil || strings.Contains(err.Error(), "T0KEN") {
		t.Errorf("error = %v; it must exist and not hold the token", err)
	}
}
