package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeToken is token-shaped on purpose: the numeric id and the colon are what
// the redactor keys on. The secret part is kept shorter than the 35 characters
// that the ADR 0002 leak check looks for, so this placeholder cannot trip CI.
const fakeToken = "1234567890:FAKE-TOKEN-FOR-TESTS-ONLY"

// newTestClient points a client at a fake Bot API server. That this is
// possible at all is the proof of the base-URL requirement: a client with the
// cloud address baked in could not be tested this way.
func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c, err := New(Config{BaseURL: srv.URL, Token: fakeToken})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, srv
}

func TestNewBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		token   string
		want    string
		wantErr error
	}{
		{"empty base url falls back to cloud", "", fakeToken, DefaultBaseURL, nil},
		{"local server is honoured", "http://telegram-bot-api:8081", fakeToken, "http://telegram-bot-api:8081", nil},
		{"trailing slash is trimmed", "http://telegram-bot-api:8081/", fakeToken, "http://telegram-bot-api:8081", nil},
		{"missing token is rejected", "", "", "", ErrNoToken},
		{"relative base url is rejected", "telegram-bot-api:8081", fakeToken, "", ErrBadBaseURL},
		{"non-http scheme is rejected", "ftp://example.invalid", fakeToken, "", ErrBadBaseURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(Config{BaseURL: tt.baseURL, Token: tt.token})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if got := c.BaseURL(); got != tt.want {
				t.Errorf("BaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSendMessageRoundTrip(t *testing.T) {
	var gotPath string
	var gotBody map[string]any

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"result":{"message_id":4242,"date":1,"chat":{"id":77,"type":"private"},"text":"hello"}}`)
	})

	markup := map[string]any{"inline_keyboard": [][]any{}}
	id, err := c.SendMessage(context.Background(), 77, "hello", markup, "")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if id != 4242 {
		t.Errorf("message id = %d, want 4242", id)
	}
	if want := "/bot" + fakeToken + "/sendMessage"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if got := gotBody["chat_id"]; got != float64(77) {
		t.Errorf("chat_id = %v, want 77", got)
	}
	if got := gotBody["text"]; got != "hello" {
		t.Errorf("text = %v, want hello", got)
	}
	if _, ok := gotBody["reply_markup"]; !ok {
		t.Error("reply_markup was not sent")
	}
	if _, ok := gotBody["token"]; ok {
		t.Error("request body carried a token field")
	}
}

func TestGetMe(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/getMe") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		io.WriteString(w, `{"ok":true,"result":{"id":99,"is_bot":true,"first_name":"Torn","username":"torn_test_bot"}}`)
	})

	me, err := c.GetMe(context.Background())
	if err != nil {
		t.Fatalf("GetMe: %v", err)
	}
	if me.ID != 99 || me.Username != "torn_test_bot" {
		t.Errorf("got id=%d username=%q, want 99 / torn_test_bot", me.ID, me.Username)
	}
}

func TestGetUpdatesQuery(t *testing.T) {
	var gotOffset, gotTimeout string

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotOffset = r.URL.Query().Get("offset")
		gotTimeout = r.URL.Query().Get("timeout")
		io.WriteString(w, `{"ok":true,"result":[{"update_id":8,"message":{"message_id":1,"date":1,"chat":{"id":5,"type":"private"},"text":"/start"}}]}`)
	})

	updates, err := c.GetUpdates(context.Background(), 4242, 5)
	if err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	if gotOffset != "4242" {
		t.Errorf("offset = %q, want 4242", gotOffset)
	}
	if gotTimeout != "5" {
		t.Errorf("timeout = %q, want 5", gotTimeout)
	}
	if len(updates) != 1 || updates[0].UpdateID != 8 {
		t.Fatalf("unexpected updates: %+v", updates)
	}
	if updates[0].Message == nil || updates[0].Message.Text != "/start" {
		t.Errorf("message not decoded: %+v", updates[0].Message)
	}
}

// TestGetUpdatesPollTimeoutBounds guards the invariant that keeps the HTTP
// timeout longer than the poll timeout: a poll longer than MaxPollTimeout is
// refused rather than silently cut short by the transport.
func TestGetUpdatesPollTimeoutBounds(t *testing.T) {
	c, err := New(Config{BaseURL: "http://telegram-bot-api:8081", Token: fakeToken})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.pollClient.Timeout <= MaxPollTimeout {
		t.Errorf("poll client timeout %s must exceed MaxPollTimeout %s", c.pollClient.Timeout, MaxPollTimeout)
	}
	if _, err := c.GetUpdates(context.Background(), 0, -1); !errors.Is(err, ErrNegativePollTimeout) {
		t.Errorf("negative timeout: got %v, want ErrNegativePollTimeout", err)
	}
	seconds := int(MaxPollTimeout/time.Second) + 1
	if _, err := c.GetUpdates(context.Background(), 0, seconds); !errors.Is(err, ErrPollTimeoutTooLong) {
		t.Errorf("oversized timeout: got %v, want ErrPollTimeoutTooLong", err)
	}
}

// TestGetUpdatesHonoursContext proves a cancelled context aborts a poll that
// the server is deliberately holding open.
func TestGetUpdatesHonoursContext(t *testing.T) {
	release := make(chan struct{})
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
		io.WriteString(w, `{"ok":true,"result":[]}`)
	})
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	if _, err := c.GetUpdates(ctx, 0, 30); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestAPIErrorFromNotOK(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
	})

	_, err := c.SendMessage(context.Background(), 1, "x", nil, "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("got %T (%v), want *APIError", err, err)
	}
	if apiErr.Code != 400 {
		t.Errorf("code = %d, want 400", apiErr.Code)
	}
	if apiErr.Method != "sendMessage" {
		t.Errorf("method = %q, want sendMessage", apiErr.Method)
	}
	if !strings.Contains(apiErr.Description, "chat not found") {
		t.Errorf("description = %q", apiErr.Description)
	}
}

// TestFloodWait covers the 429 paths. The retry_after schema is unverified
// (ADR 0003), so every shape the client tolerates is pinned here.
func TestFloodWait(t *testing.T) {
	tests := []struct {
		name   string
		status int
		header string
		body   string
		want   time.Duration
	}{
		{
			name:   "retry_after in parameters",
			status: http.StatusTooManyRequests,
			body:   `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":17}}`,
			want:   17 * time.Second,
		},
		{
			name:   "retry_after quoted as a string",
			status: http.StatusTooManyRequests,
			body:   `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":"9"}}`,
			want:   9 * time.Second,
		},
		{
			name:   "retry_after at the top level",
			status: http.StatusTooManyRequests,
			body:   `{"ok":false,"error_code":429,"description":"Too Many Requests","retry_after":11}`,
			want:   11 * time.Second,
		},
		{
			name:   "Retry-After header only",
			status: http.StatusTooManyRequests,
			header: "13",
			body:   `{"ok":false,"error_code":429,"description":"Too Many Requests"}`,
			want:   13 * time.Second,
		},
		{
			name:   "no retry_after falls back to the default",
			status: http.StatusTooManyRequests,
			body:   `{"ok":false,"error_code":429,"description":"Too Many Requests"}`,
			want:   DefaultFloodWait,
		},
		{
			name:   "empty parameters object falls back to the default",
			status: http.StatusTooManyRequests,
			body:   `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{}}`,
			want:   DefaultFloodWait,
		},
		{
			name:   "unusable retry_after falls back to the default",
			status: http.StatusTooManyRequests,
			body:   `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":0}}`,
			want:   DefaultFloodWait,
		},
		{
			name:   "non-envelope 429 body still yields a flood wait",
			status: http.StatusTooManyRequests,
			header: "21",
			body:   `<html>too many requests</html>`,
			want:   21 * time.Second,
		},
		{
			name:   "error_code 429 without HTTP 429",
			status: http.StatusOK,
			body:   `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":3}}`,
			want:   3 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if tt.header != "" {
					w.Header().Set("Retry-After", tt.header)
				}
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			})

			_, err := c.SendMessage(context.Background(), 1, "x", nil, "")
			var flood *FloodWaitError
			if !errors.As(err, &flood) {
				t.Fatalf("got %T (%v), want *FloodWaitError", err, err)
			}
			if flood.RetryAfter != tt.want {
				t.Errorf("RetryAfter = %s, want %s", flood.RetryAfter, tt.want)
			}
			if flood.RetryAfter <= 0 {
				t.Error("RetryAfter must never be zero: retrying immediately after a 429 is the worst reaction")
			}
		})
	}
}

// TestErrorsNeverContainToken is the requirement from ADR 0002: the token
// lives in the request path, so it is one careless %v away from a log file.
func TestErrorsNeverContainToken(t *testing.T) {
	// A server that is already closed produces a transport error carrying the
	// full request URL, token and all.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	deadClient, err := New(Config{BaseURL: deadURL, Token: fakeToken})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// A server that echoes the token back inside a description, which is the
	// other way a credential travels back into a log line.
	echoClient, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"ok":false,"error_code":400,"description":"Unauthorized for `+fakeToken+`"}`)
	})

	tests := []struct {
		name string
		call func() error
	}{
		{"transport failure", func() error {
			_, err := deadClient.SendMessage(context.Background(), 1, "x", nil, "")
			return err
		}},
		{"api error echoing the token", func() error {
			_, err := echoClient.SendMessage(context.Background(), 1, "x", nil, "")
			return err
		}},
		{"getMe against a dead server", func() error {
			_, err := deadClient.GetMe(context.Background())
			return err
		}},
		{"getUpdates against a dead server", func() error {
			_, err := deadClient.GetUpdates(context.Background(), 1, 0)
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), fakeToken) {
				t.Fatalf("error text leaked the token: %q", err.Error())
			}
			if strings.Contains(err.Error(), "FAKE-TOKEN-FOR-TESTS-ONLY") {
				t.Fatalf("error text leaked the token secret: %q", err.Error())
			}
			if !strings.Contains(err.Error(), redactedPlaceholder) {
				t.Errorf("expected %s in %q", redactedPlaceholder, err.Error())
			}
		})
	}

	if s := deadClient.String(); strings.Contains(s, fakeToken) || strings.Contains(s, "FAKE-TOKEN") {
		t.Errorf("String() leaked the token: %q", s)
	}
}

func TestRedact(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain token", "1234567890:FAKE-TOKEN-FOR-TESTS-ONLY", redactedPlaceholder},
		{
			"token inside a url",
			"Post \"http://telegram-bot-api:8081/bot1234567890:FAKE-TOKEN-FOR-TESTS-ONLY/sendMessage\": refused",
			"Post \"http://telegram-bot-api:8081/bot" + redactedPlaceholder + "/sendMessage\": refused",
		},
		{"nothing to redact", "chat not found", "chat not found"},
		{"empty", "", ""},
		{"a chat id is not a token", "chat_id -1001234567890 not found", "chat_id -1001234567890 not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redact(tt.in); got != tt.want {
				t.Errorf("redact(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestEditMessageTextAndAnswerCallbackQuery(t *testing.T) {
	var seen []string
	var bodies []map[string]any

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		seen = append(seen, parts[len(parts)-1])
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		bodies = append(bodies, body)
		io.WriteString(w, `{"ok":true,"result":true}`)
	})

	if err := c.EditMessageText(context.Background(), 5, 6, "updated", nil, ""); err != nil {
		t.Fatalf("EditMessageText: %v", err)
	}
	if err := c.AnswerCallbackQuery(context.Background(), "cb-1", "done"); err != nil {
		t.Fatalf("AnswerCallbackQuery: %v", err)
	}

	if len(seen) != 2 || seen[0] != "editMessageText" || seen[1] != "answerCallbackQuery" {
		t.Fatalf("methods called = %v", seen)
	}
	if bodies[0]["message_id"] != float64(6) {
		t.Errorf("message_id = %v, want 6", bodies[0]["message_id"])
	}
	if _, ok := bodies[0]["reply_markup"]; ok {
		t.Error("nil reply_markup must be omitted, not sent as null")
	}
	if bodies[1]["callback_query_id"] != "cb-1" {
		t.Errorf("callback_query_id = %v", bodies[1]["callback_query_id"])
	}
}

// TestBadHTMLFallsBackToPlainText proves the one thing that must never
// happen: a screen's own HTML mistake reaching a player as nothing at all.
// Telegram's refusal of unparseable entities is retried once, in plain text
// with the tags stripped and the entities decoded, so the player still
// reads the screen — a title without its bold, not a missing message.
func TestBadHTMLFallsBackToPlainText(t *testing.T) {
	var bodies []map[string]any
	attempt := 0

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		bodies = append(bodies, body)
		attempt++
		if attempt == 1 {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities: Unsupported start tag \"x\""}`)
			return
		}
		io.WriteString(w, `{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":7,"type":"private"},"text":"hi"}}`)
	})

	id, err := c.SendMessage(context.Background(), 7, "<b>Ada &amp; Sons</b>", nil, "HTML")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if id != 1 {
		t.Errorf("message id = %d, want 1", id)
	}
	if len(bodies) != 2 {
		t.Fatalf("attempts = %d, want 2 (the HTML try, then the plain-text fallback)", len(bodies))
	}
	if bodies[0]["parse_mode"] != "HTML" {
		t.Errorf("first attempt parse_mode = %v, want HTML", bodies[0]["parse_mode"])
	}
	if _, ok := bodies[1]["parse_mode"]; ok {
		t.Errorf("fallback attempt still sets parse_mode: %v", bodies[1]["parse_mode"])
	}
	if bodies[1]["text"] != "Ada & Sons" {
		t.Errorf("fallback text = %q, want the tags stripped and the entity decoded: %q", bodies[1]["text"], "Ada & Sons")
	}

	// EditMessageText and SendMessageWith fall back the same way.
	attempt = 0
	bodies = nil
	if err := c.EditMessageText(context.Background(), 7, 1, "<b>bad</b>", nil, "HTML"); err != nil {
		t.Fatalf("EditMessageText: %v", err)
	}
	if len(bodies) != 2 || bodies[1]["text"] != "bad" {
		t.Errorf("EditMessageText did not fall back to plain text: %v", bodies)
	}

	attempt = 0
	bodies = nil
	if _, err := c.SendMessageWith(context.Background(), 7, "<b>bad</b>", nil, SendOptions{ParseMode: "HTML"}); err != nil {
		t.Fatalf("SendMessageWith: %v", err)
	}
	if len(bodies) != 2 || bodies[1]["text"] != "bad" {
		t.Errorf("SendMessageWith did not fall back to plain text: %v", bodies)
	}
}

// TestLogOutAndClose covers the two migration methods from ADR 0003.
func TestLogOutAndClose(t *testing.T) {
	var seen []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		seen = append(seen, parts[len(parts)-1])
		io.WriteString(w, `{"ok":true,"result":true}`)
	})

	if err := c.LogOut(context.Background()); err != nil {
		t.Fatalf("LogOut: %v", err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(seen) != 2 || seen[0] != "logOut" || seen[1] != "close" {
		t.Fatalf("methods called = %v, want [logOut close]", seen)
	}
}

// TestIsLocalFilePath pins the distinction from ADR 0003 decision 4 between a
// local absolute path and a cloud relative path.
func TestIsLocalFilePath(t *testing.T) {
	c, err := New(Config{Token: fakeToken})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"local absolute path", "/var/lib/telegram-bot-api/99/photos/file_0.jpg", true},
		{"cloud relative path", "photos/file_0.jpg", false},
		{"explicit url", "https://example.invalid/file_0.jpg", false},
		{"empty", "", false},
		{"windows absolute path", `C:\telegram\file_0.jpg`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.IsLocalFilePath(tt.path); got != tt.want {
				t.Errorf("IsLocalFilePath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestUndecodableBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "<html>bad gateway</html>")
	})

	_, err := c.SendMessage(context.Background(), 1, "x", nil, "")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error should mention the status: %q", err.Error())
	}
}
