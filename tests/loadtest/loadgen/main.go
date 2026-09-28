// Command loadgen drives the horizontal-scaling proof described in the
// repository's scale-out report. It is throwaway test tooling, not part of
// the product, and talks to nothing but this local docker compose stack.
//
// Two paths are exercised, matching how a real player's actions enter the
// system:
//
//   - "telegram": synthetic incoming messages are pushed into mocktelegram's
//     queue, exactly as a real Telegram update would arrive; whichever
//     gateway replica holds the bot lease polls them, resolves identity,
//     and publishes a command onto NATS's GAME_COMMANDS stream, same as
//     production.
//   - "clientapi": a synthetic Telegram Mini App sign-in (a real, correctly
//     signed Telegram initData string, using the mock bot's own token — no
//     real secret involved) authenticates against clientapi, and the
//     resulting session runs player.profile.get through
//     POST /api/v1/command, which is clientapi's own path onto the same
//     NATS stream.
//
// Two things this tool checks are the point of the exercise:
//
//   - "unique": N distinct synthetic players each /start once. Every one of
//     them must end up with exactly one players row and exactly one
//     starting-cash credit — nothing lost, nothing double-processed.
//   - "dup": one synthetic player's /start is injected many times at once,
//     simulating Telegram's at-least-once delivery plus a shaky client
//     retrying. However many gateway and game replicas are running, exactly
//     one players row and one starting-cash credit must result.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	mode := flag.String("mode", "telegram", "telegram | clientapi | dup")
	mockURL := flag.String("mock-url", "http://127.0.0.1:58099", "mocktelegram base URL")
	clientAPIURL := flag.String("clientapi-url", "http://127.0.0.1:58091", "clientapi base URL (through the load balancer)")
	botToken := flag.String("bot-token", "", "the fake bot token (matches telegram_bots.token_secret_ref's value)")
	n := flag.Int("n", 200, "number of distinct synthetic players")
	concurrency := flag.Int("concurrency", 20, "concurrent workers")
	dupCopies := flag.Int("dup-copies", 8, "for -mode=dup, how many times the same message is injected at once")
	userIDBase := flag.Int64("user-id-base", 900000000, "synthetic telegram user ids start here")
	flag.Parse()

	switch *mode {
	case "telegram":
		runTelegram(*mockURL, *n, *concurrency, *userIDBase)
	case "dup":
		runDup(*mockURL, *dupCopies, *userIDBase)
	case "clientapi":
		if *botToken == "" {
			log.Fatal("clientapi mode needs -bot-token")
		}
		runClientAPI(*clientAPIURL, *botToken, *n, *concurrency, *userIDBase)
	default:
		log.Fatalf("unknown -mode %q", *mode)
	}
}

// ---- telegram path ---------------------------------------------------------

type injectItem struct {
	UserID   int64  `json:"user_id"`
	ChatID   int64  `json:"chat_id"`
	Username string `json:"username"`
	Text     string `json:"text"`
}

func inject(mockURL string, items []injectItem) error {
	raw, err := json.Marshal(items)
	if err != nil {
		return err
	}
	resp, err := http.Post(mockURL+"/_test/inject", "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("inject: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

func runTelegram(mockURL string, n, concurrency int, userIDBase int64) {
	start := time.Now()
	var wg sync.WaitGroup
	var ok, fail atomic.Int64
	work := make(chan int64, n)
	for i := 0; i < n; i++ {
		work <- userIDBase + int64(i)
	}
	close(work)

	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range work {
				err := inject(mockURL, []injectItem{{UserID: id, ChatID: id, Username: fmt.Sprintf("load%d", id), Text: "/start"}})
				if err != nil {
					fail.Add(1)
					log.Printf("inject failed for %d: %v", id, err)
					continue
				}
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	fmt.Printf("telegram: injected %d ok, %d failed, in %s (%.1f/s)\n",
		ok.Load(), fail.Load(), elapsed, float64(ok.Load())/elapsed.Seconds())
	fmt.Printf("first_user_id=%d last_user_id=%d\n", userIDBase, userIDBase+int64(n)-1)
}

func runDup(mockURL string, copies int, userIDBase int64) {
	id := userIDBase
	items := make([]injectItem, 0, copies)
	for i := 0; i < copies; i++ {
		items = append(items, injectItem{UserID: id, ChatID: id, Username: "dup", Text: "/start"})
	}
	start := time.Now()
	if err := inject(mockURL, items); err != nil {
		log.Fatalf("dup inject: %v", err)
	}
	fmt.Printf("dup: injected %d copies of the same /start for telegram_user_id=%d in %s\n", copies, id, time.Since(start))
}

// ---- clientapi path ---------------------------------------------------------

// signInitData builds a Telegram Mini App initData string signed with token,
// following https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app.
// The token is the mock bot's own, generated for this test stack — never a
// real Telegram secret.
func signInitData(token string, userID int64, username string, now time.Time) string {
	user := map[string]any{"id": userID, "first_name": "load", "username": username, "is_bot": false}
	userJSON, _ := json.Marshal(user)

	fields := map[string]string{
		"user":      string(userJSON),
		"auth_date": strconv.FormatInt(now.Unix(), 10),
		"query_id":  fmt.Sprintf("AA%d%d", userID, rand.Int63()),
	}

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+fields[k])
	}
	dataCheckString := strings.Join(lines, "\n")

	secretKey := hmac.New(sha256.New, []byte("WebAppData"))
	secretKey.Write([]byte(token))

	mac := hmac.New(sha256.New, secretKey.Sum(nil))
	mac.Write([]byte(dataCheckString))
	hash := hex.EncodeToString(mac.Sum(nil))

	v := url.Values{}
	for k, val := range fields {
		v.Set(k, val)
	}
	v.Set("hash", hash)
	return v.Encode()
}

type sessionResp struct {
	AccessToken string `json:"access_token"`
}

// httpClient reuses connections across every worker goroutine. The default
// transport caps idle connections at 2 per host, which starves a
// -concurrency above that with constant new TCP handshakes and turns this
// into a load generator benchmark rather than a clientapi one; raised here
// so the numbers this tool reports reflect the server side, not its own
// client.
var httpClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        512,
		MaxIdleConnsPerHost: 512,
		IdleConnTimeout:     90 * time.Second,
	},
}

func postJSON(url string, body any, bearer, forwardedFor string) (int, []byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if forwardedFor != "" {
		// Each synthetic player gets its own address, so clientapi's
		// per-address sign-in and command rate limits (client.yml:
		// sign_ins_per_minute, commands_per_minute) do not turn this
		// throughput test into a single-bucket bottleneck — every real
		// player already gets their own address the same way, through
		// nginx's X-Forwarded-For (client.trusted_proxies must list the
		// proxy; see deployments/compose.scale-test.yml).
		req.Header.Set("X-Forwarded-For", forwardedFor)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out, nil
}

func runClientAPI(baseURL, botToken string, n, concurrency int, userIDBase int64) {
	start := time.Now()
	var wg sync.WaitGroup
	var signInOK, signInFail, cmdOK, cmdFail atomic.Int64
	work := make(chan int64, n)
	for i := 0; i < n; i++ {
		work <- userIDBase + int64(i)
	}
	close(work)

	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range work {
				fwd := syntheticAddress(id)
				initData := signInitData(botToken, id, fmt.Sprintf("load%d", id), time.Now())
				status, body, err := postJSON(baseURL+"/api/v1/auth/telegram",
					map[string]string{"init_data": initData, "device_name": "loadgen"}, "", fwd)
				if err != nil || status != http.StatusOK {
					signInFail.Add(1)
					log.Printf("sign-in failed for %d: status=%d err=%v body=%s", id, status, err, truncate(body))
					continue
				}
				signInOK.Add(1)
				var sess sessionResp
				if err := json.Unmarshal(body, &sess); err != nil || sess.AccessToken == "" {
					cmdFail.Add(1)
					continue
				}
				status, body, err = postJSON(baseURL+"/api/v1/command",
					map[string]any{"command": "player.profile.get", "args": map[string]any{}}, sess.AccessToken, fwd)
				if err != nil || status != http.StatusOK {
					cmdFail.Add(1)
					log.Printf("command failed for %d: status=%d err=%v body=%s", id, status, err, truncate(body))
					continue
				}
				cmdOK.Add(1)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	fmt.Printf("clientapi: sign-in ok=%d fail=%d; command ok=%d fail=%d; in %s (%.1f commands/s)\n",
		signInOK.Load(), signInFail.Load(), cmdOK.Load(), cmdFail.Load(), elapsed, float64(cmdOK.Load())/elapsed.Seconds())
}

// syntheticAddress gives each synthetic player its own source address for
// X-Forwarded-For, so clientapi's per-address rate limits bucket them
// separately instead of all landing under the load balancer's one real
// socket address.
func syntheticAddress(id int64) string {
	return fmt.Sprintf("10.%d.%d.%d", (id>>16)&0xff, (id>>8)&0xff, id&0xff)
}

func truncate(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "..."
	}
	return string(b)
}
