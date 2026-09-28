// Command mocktelegram is a throwaway stand-in for the Telegram Bot API,
// used only by the local horizontal-scaling proof (see the repository's
// scale-out report). It exists so the gateway's real poll-and-reply loop —
// getUpdates, route, publish to NATS, wait for the response, sendMessage —
// can be driven end to end without a real bot token or any contact with
// Telegram's servers.
//
// It implements exactly the methods internal/gateway/telegram/client calls:
// getMe, getUpdates, sendMessage, editMessageText, answerCallbackQuery,
// logOut, close. Everything else in the Bot API is out of scope.
//
// Two extra endpoints exist only for the test harness, under /_test/, and
// are not part of the Bot API surface:
//
//	POST /_test/inject  — enqueue one or more synthetic incoming messages,
//	                      as if a player had just sent them.
//	GET  /_test/stats   — counters the load generator and the report use to
//	                      check nothing was lost and nothing double-sent.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	client "github.com/mrjvadi/torncity/internal/gateway/telegram/client"
)

var methodPath = regexp.MustCompile(`^/bot[^/]+/([A-Za-z]+)$`)

// apiResponse mirrors the envelope internal/gateway/telegram/client decodes.
type apiResponse struct {
	OK     bool `json:"ok"`
	Result any  `json:"result"`
}

// server holds every piece of mutable state. A single mutex is enough: this
// is a test double serving a handful of gateway replicas, not a production
// service under real concurrency requirements.
type server struct {
	mu      sync.Mutex
	queue   []client.Update
	updSeq  int64
	msgSeq  int64
	waiters []chan struct{} // woken whenever the queue grows

	sendCount   atomic.Int64
	editCount   atomic.Int64
	injectCount atomic.Int64
}

func newServer() *server { return &server{} }

func (s *server) wake() {
	s.mu.Lock()
	waiters := s.waiters
	s.waiters = nil
	s.mu.Unlock()
	for _, w := range waiters {
		close(w)
	}
}

// injectRequest is one synthetic incoming message.
type injectRequest struct {
	UserID   int64  `json:"user_id"`
	ChatID   int64  `json:"chat_id"`
	Username string `json:"username"`
	Text     string `json:"text"`
}

func (s *server) handleInject(w http.ResponseWriter, r *http.Request) {
	var batch []injectRequest
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	first := s.updSeq + 1
	now := time.Now().Unix()
	for _, in := range batch {
		s.updSeq++
		s.msgSeq++
		s.queue = append(s.queue, client.Update{
			UpdateID: s.updSeq,
			Message: &client.Message{
				MessageID: s.msgSeq,
				From: &client.User{
					ID:        in.UserID,
					FirstName: "load",
					Username:  in.Username,
				},
				Chat: client.Chat{ID: in.ChatID, Type: "private"},
				Date: now,
				Text: in.Text,
			},
		})
	}
	s.mu.Unlock()
	s.injectCount.Add(int64(len(batch)))
	s.wake()

	writeJSON(w, map[string]any{"ok": true, "count": len(batch), "first_update_id": first})
}

func (s *server) handleStats(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	pending := len(s.queue)
	s.mu.Unlock()
	writeJSON(w, map[string]any{
		"injected":     s.injectCount.Load(),
		"pending":      pending,
		"send_message": s.sendCount.Load(),
		"edit_message": s.editCount.Load(),
	})
}

// handleGetUpdates implements long polling: it blocks (bounded by the
// caller's timeout) until the queue has something at or after offset, or the
// timeout elapses, matching what a real getUpdates poller expects.
func (s *server) handleGetUpdates(w http.ResponseWriter, r *http.Request) {
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	timeoutSeconds, _ := strconv.Atoi(r.URL.Query().Get("timeout"))
	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)

	for {
		s.mu.Lock()
		var out []client.Update
		kept := s.queue[:0:0]
		for _, u := range s.queue {
			if offset == 0 || u.UpdateID >= offset {
				out = append(out, u)
				kept = append(kept, u)
			}
		}
		s.queue = kept
		if len(out) > 0 || timeoutSeconds == 0 || time.Now().After(deadline) {
			s.mu.Unlock()
			writeJSON(w, apiResponse{OK: true, Result: out})
			return
		}
		ch := make(chan struct{})
		s.waiters = append(s.waiters, ch)
		s.mu.Unlock()

		select {
		case <-ch:
		case <-time.After(time.Until(deadline)):
		case <-r.Context().Done():
			return
		}
	}
}

func (s *server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.sendCount.Add(1)
	s.mu.Lock()
	s.msgSeq++
	id := s.msgSeq
	s.mu.Unlock()
	writeJSON(w, apiResponse{OK: true, Result: client.Message{
		MessageID: id,
		Chat:      client.Chat{ID: body.ChatID, Type: "private"},
		Date:      time.Now().Unix(),
		Text:      body.Text,
	}})
}

func (s *server) handleMethod(w http.ResponseWriter, r *http.Request) {
	m := methodPath.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	switch m[1] {
	case "getUpdates":
		s.handleGetUpdates(w, r)
	case "sendMessage":
		s.handleSendMessage(w, r)
	case "editMessageText":
		s.editCount.Add(1)
		writeJSON(w, apiResponse{OK: true, Result: true})
	case "getMe":
		writeJSON(w, apiResponse{OK: true, Result: client.User{ID: 1, IsBot: true, FirstName: "mock"}})
	case "answerCallbackQuery", "logOut", "close":
		writeJSON(w, apiResponse{OK: true, Result: true})
	default:
		writeJSON(w, apiResponse{OK: true, Result: true})
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	addr := flag.String("addr", ":8081", "listen address")
	flag.Parse()

	s := newServer()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /_test/inject", s.handleInject)
	mux.HandleFunc("GET /_test/stats", s.handleStats)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, map[string]bool{"ok": true}) })
	mux.HandleFunc("/", s.handleMethod)

	log.Printf("mocktelegram listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}
