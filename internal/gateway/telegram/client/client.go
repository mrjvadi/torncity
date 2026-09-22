// Package client is a minimal Telegram Bot API client for the gateway.
//
// It exists as its own package for one decisive reason recorded in
// docs/adr/0003-local-bot-api-server.md: this deployment does not talk to
// https://api.telegram.org. It talks to a self-hosted tdlib/telegram-bot-api
// instance reachable at an address such as http://telegram-bot-api:8081. The
// ADR states that no part of the code may hardcode the cloud address, so the
// base URL is a Config field and the cloud address is only the default used
// when no local server is configured.
//
// # Local server differences that callers must know about
//
// A local server is HTTP-only and must never be exposed to the internet; TLS
// is unnecessary because the gateway and the server share a Docker network.
//
// More importantly for anyone adding media support later: in --local mode a
// getFile response carries an ABSOLUTE PATH ON THE SERVER'S FILESYSTEM in
// file_path, not a relative path to be appended to a download URL. Cloud mode
// returns something like "photos/file_0.jpg" which the caller must fetch over
// HTTP; local mode returns something like "/var/lib/telegram-bot-api/<bot
// id>/photos/file_0.jpg" which is already on disk. Code that assumes the cloud
// shape fails silently against the local server. Client.IsLocalFilePath exists
// so that branch is explicit rather than accidental.
//
// # Secrets
//
// Per docs/adr/0002-secret-management.md the bot token must never reach a log,
// a metric, a NATS payload or an error message. The token is unexported, no
// exported type in this package carries it, and every error string produced
// here is passed through redact first.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the cloud Bot API address. It is a fallback only: the real
// deployment sets Config.BaseURL to the local server (ADR 0003, decision 1).
const DefaultBaseURL = "https://api.telegram.org"

// The operational values below are the DEFAULTS this package falls back to
// when Config leaves the matching field at zero. They are not the values a
// deployment runs: configs/config.yml declares those under `telegram:` and
// internal/config injects them through Config. They are kept here so a caller
// that supplies nothing still gets the behaviour this package shipped with,
// and so config.Defaults() has something to mirror.
const (
	// DefaultRequestTimeout bounds an ordinary (non-polling) call.
	DefaultRequestTimeout = 30 * time.Second

	// MaxPollTimeout is the largest long-poll timeout GetUpdates accepts. It
	// exists so the polling HTTP client can be given a fixed transport timeout
	// that is provably larger than any poll it will ever be asked to make.
	MaxPollTimeout = 120 * time.Second

	// PollTimeoutGrace is added on top of the requested poll timeout before the
	// HTTP layer gives up. Without it the classic bug appears: an HTTP timeout
	// shorter than or equal to the long-poll timeout aborts every single poll
	// just before the server would have answered, and the bot looks dead while
	// every request "works".
	PollTimeoutGrace = 15 * time.Second

	// DefaultFloodWait is used when the server answers 429 but gives no usable
	// retry_after. Five seconds is deliberately conservative: ADR 0001 records
	// the working assumption of one message per second per chat, so waiting
	// several seconds costs little, while retrying too early risks a longer
	// ban. ADR 0003 tells the flood handler to behave conservatively exactly
	// because the retry_after schema is unconfirmed.
	DefaultFloodWait = 5 * time.Second

	// maxResponseBytes caps how much of a response body is read, so a
	// misbehaving or wrong endpoint cannot exhaust gateway memory.
	maxResponseBytes = 8 << 20
)

// Configuration failures.
var (
	ErrNoToken    = errors.New("telegram: token is required")
	ErrBadBaseURL = errors.New("telegram: base_url must be an absolute http or https URL")

	// ErrPollTimeoutTooLong guards the invariant that the polling HTTP timeout
	// stays longer than the poll itself.
	ErrPollTimeoutTooLong = errors.New("telegram: poll timeout exceeds MaxPollTimeout")

	// ErrNegativePollTimeout rejects a nonsensical long-poll timeout.
	ErrNegativePollTimeout = errors.New("telegram: poll timeout must not be negative")

	// ErrPollHTTPTimeoutTooShort rejects a polling transport timeout that does
	// not outlast the longest poll the client would accept. It exists because
	// the one way to misconfigure this client that produces no error, no log
	// line and no failed metric is to hand the polling transport the ordinary
	// request timeout: every getUpdates is then aborted just before Telegram
	// answers, and the bot stops receiving updates while looking healthy.
	ErrPollHTTPTimeoutTooShort = errors.New("telegram: poll_http_timeout must exceed max_poll_timeout")
)

// Config is everything the client needs. BaseURL is the field that matters:
// leaving it empty selects the cloud API, and the deployment sets it to the
// local server.
type Config struct {
	// BaseURL is the Bot API root, e.g. http://telegram-bot-api:8081.
	// Empty means DefaultBaseURL.
	BaseURL string

	// Token is the bot token. It is copied into an unexported field and is
	// never marshalled, logged or echoed back in an error.
	Token string

	// RequestTimeout bounds ordinary calls. Zero means DefaultRequestTimeout.
	// It is NOT applied to GetUpdates; see PollHTTPTimeout.
	RequestTimeout time.Duration

	// MaxPollTimeout is the largest long-poll window GetUpdates will accept.
	// Zero means the MaxPollTimeout default.
	MaxPollTimeout time.Duration

	// PollTimeoutGrace is how much longer than the requested poll window one
	// getUpdates call is allowed to run. Zero means the PollTimeoutGrace
	// default.
	PollTimeoutGrace time.Duration

	// PollHTTPTimeout is the transport timeout of the polling HTTP client.
	//
	// It is a field of its own, and not derived quietly from the two above,
	// because this is the value that decides whether long polling works at
	// all. Zero means MaxPollTimeout + PollTimeoutGrace, which is what
	// config.Telegram.PollHTTPTimeout() returns; anything at or below
	// MaxPollTimeout is rejected with ErrPollHTTPTimeoutTooShort rather than
	// accepted into a bot that silently stops receiving updates.
	PollHTTPTimeout time.Duration

	// DefaultFloodWait is the pause used when a 429 carries no usable
	// retry_after. Zero means the DefaultFloodWait default.
	DefaultFloodWait time.Duration

	// HTTPClient, when set, is used as the template for both the ordinary and
	// the polling HTTP client, so callers can inject a transport. Its Timeout
	// field is overridden on both copies.
	HTTPClient *http.Client
}

// Client talks to one bot's Bot API endpoint.
//
// It holds no exported field, so no reflection-based encoder and no %+v in a
// log line can ever reach the token.
type Client struct {
	baseURL string
	token   string

	// The operational values this client runs with, resolved once in New so
	// no method has to re-apply a fallback.
	maxPollTimeout   time.Duration
	pollTimeoutGrace time.Duration
	defaultFloodWait time.Duration

	httpClient *http.Client
	pollClient *http.Client
}

// New builds a client, applying DefaultBaseURL when Config.BaseURL is empty.
func New(cfg Config) (*Client, error) {
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil, ErrNoToken
	}

	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	base = strings.TrimRight(base, "/")

	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, ErrBadBaseURL
	}

	requestTimeout := cfg.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = DefaultRequestTimeout
	}
	maxPollTimeout := cfg.MaxPollTimeout
	if maxPollTimeout <= 0 {
		maxPollTimeout = MaxPollTimeout
	}
	pollTimeoutGrace := cfg.PollTimeoutGrace
	if pollTimeoutGrace <= 0 {
		pollTimeoutGrace = PollTimeoutGrace
	}
	floodWait := cfg.DefaultFloodWait
	if floodWait <= 0 {
		floodWait = DefaultFloodWait
	}

	pollHTTPTimeout := cfg.PollHTTPTimeout
	if pollHTTPTimeout <= 0 {
		pollHTTPTimeout = maxPollTimeout + pollTimeoutGrace
	}
	if pollHTTPTimeout <= maxPollTimeout {
		// Refused rather than tolerated: see ErrPollHTTPTimeoutTooShort.
		return nil, fmt.Errorf("%w: poll_http_timeout is %s, max_poll_timeout is %s",
			ErrPollHTTPTimeoutTooShort, pollHTTPTimeout, maxPollTimeout)
	}

	ordinary := cloneHTTPClient(cfg.HTTPClient)
	ordinary.Timeout = requestTimeout

	// The polling client gets its own copy with a transport timeout that is
	// larger than any poll GetUpdates will accept. Sharing `ordinary` here is
	// the bug this split exists to prevent.
	polling := cloneHTTPClient(cfg.HTTPClient)
	polling.Timeout = pollHTTPTimeout

	return &Client{
		baseURL:          base,
		token:            token,
		maxPollTimeout:   maxPollTimeout,
		pollTimeoutGrace: pollTimeoutGrace,
		defaultFloodWait: floodWait,
		httpClient:       ordinary,
		pollClient:       polling,
	}, nil
}

func cloneHTTPClient(src *http.Client) *http.Client {
	if src == nil {
		return &http.Client{}
	}
	dup := *src
	return &dup
}

// BaseURL reports the configured API root. It is safe to log: it contains no
// token, because the token lives in the path of each request, not in the root.
func (c *Client) BaseURL() string { return c.baseURL }

// String renders the client without its token, so an accidental %s or %v in a
// log line cannot leak the credential.
func (c *Client) String() string {
	return "telegram.Client(base_url=" + c.baseURL + ")"
}

// IsLocalFilePath reports whether a getFile file_path is an absolute path on
// the Bot API server's filesystem (what --local mode returns) rather than the
// relative path the cloud API returns for a download URL.
//
// ADR 0003 decision 4: media code must accept both shapes. Use this to branch:
// true means open the file from disk (the gateway and the server share a
// volume), false means build a download URL of the form
// <base>/file/bot<token>/<file_path> and fetch it over HTTP.
func (c *Client) IsLocalFilePath(filePath string) bool {
	if filePath == "" {
		return false
	}
	// A cloud file_path is relative ("photos/file_0.jpg"); an explicit URL is
	// never a local path either.
	if strings.Contains(filePath, "://") {
		return false
	}
	if strings.HasPrefix(filePath, "/") {
		return true
	}
	// Windows-style absolute path, for completeness: C:\... or C:/...
	if len(filePath) >= 3 && filePath[1] == ':' && (filePath[2] == '\\' || filePath[2] == '/') {
		return true
	}
	return false
}

// methodURL builds the endpoint for one Bot API method. The token is part of
// the path; that is how the Bot API authenticates, and it is precisely why
// every error derived from a URL must be redacted.
func (c *Client) methodURL(method string) string {
	return c.baseURL + "/bot" + c.token + "/" + method
}

// errorf builds an error whose text has been run through redaction.
func (c *Client) errorf(format string, args ...any) error {
	return errors.New(c.redact(fmt.Sprintf(format, args...)))
}

// redact removes this client's token as well as anything token-shaped.
func (c *Client) redact(s string) string {
	if c != nil && c.token != "" {
		s = strings.ReplaceAll(s, c.token, redactedPlaceholder)
	}
	return redact(s)
}

// do performs one Bot API call.
//
// query carries URL parameters (used by getUpdates so the poll parameters are
// visible on the request line); body, when non-nil, is marshalled as the JSON
// request body. result, when non-nil, receives the decoded "result" field.
func (c *Client) do(ctx context.Context, httpClient *http.Client, method string, query url.Values, body any, result any) error {
	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return c.errorf("telegram: encoding %s request: %v", method, err)
		}
		reqBody = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.methodURL(method), reqBody)
	if err != nil {
		return c.errorf("telegram: building %s request: %v", method, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if len(query) > 0 {
		req.URL.RawQuery = query.Encode()
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		// A cancelled or expired context is returned unwrapped so callers can
		// match it with errors.Is. It can carry no token.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// Everything else may embed the request URL, which contains the token.
		return c.errorf("telegram: %s request failed: %v", method, err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return c.errorf("telegram: reading %s response: %v", method, err)
	}

	return c.interpret(method, resp, payload, result)
}

// interpret turns an HTTP response into either a decoded result or a typed
// error. It is separate from do so the decision table can be read in one go.
func (c *Client) interpret(method string, resp *http.Response, payload []byte, result any) error {
	var env apiResponse
	decodeErr := json.Unmarshal(payload, &env)

	// 429 is decided before the envelope, because a proxy or the local server
	// may answer 429 with a body that is not a Bot API envelope at all.
	if resp.StatusCode == http.StatusTooManyRequests || env.ErrorCode == http.StatusTooManyRequests {
		wait, ok := retryAfter(env, resp.Header)
		if !ok {
			wait = c.defaultFloodWait
		}
		return &FloodWaitError{
			Method:      method,
			RetryAfter:  wait,
			Description: c.redact(env.Description),
		}
	}

	if decodeErr != nil {
		return c.errorf("telegram: %s returned status %d with an undecodable body: %v",
			method, resp.StatusCode, decodeErr)
	}

	if !env.OK {
		code := env.ErrorCode
		if code == 0 {
			code = resp.StatusCode
		}
		return &APIError{
			Method:      method,
			Code:        code,
			Description: c.redact(env.Description),
		}
	}

	if resp.StatusCode != http.StatusOK {
		return c.errorf("telegram: %s reported ok with unexpected status %d", method, resp.StatusCode)
	}

	if result == nil || len(env.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(env.Result, result); err != nil {
		return c.errorf("telegram: decoding %s result: %v", method, err)
	}
	return nil
}
