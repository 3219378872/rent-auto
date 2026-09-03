package uu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/platform"
)

const apiBase = "https://api.youpin898.com"

// Envelope codes / business codes observed on the wire.
const (
	codeOK              = 0
	codeAuthExpired     = 84101   // login state invalid
	codeRiskControl     = 84104   // rate limited / risk control
	codeVersionBlocked  = 5050    // version/registration gate: third-party SMS login refused
	codeCaptchaRequired = 1110205 // behavior-verify (slider/click captcha) challenge
)

// ErrUKExpired maps HTTP 405 responses (upstream: "UK token expired").
var ErrUKExpired = errors.New("uu: uk token expired")

type noopLimiter struct{}

func (noopLimiter) Wait(context.Context) error { return nil }

// Client talks to youpin898. Create with NewClient; safe for concurrent use.
type Client struct {
	http    *http.Client
	token   string
	device  string
	userID  int64
	nick    string
	limiter platform.Limiter
	log     *slog.Logger
}

type Option func(*Client)

func WithHTTPClient(h *http.Client) Option  { return func(c *Client) { c.http = h } }
func WithLimiter(l platform.Limiter) Option { return func(c *Client) { c.limiter = l } }
func WithLogger(l *slog.Logger) Option      { return func(c *Client) { c.log = l } }

func NewClient(ctx context.Context, token string, opts ...Option) (*Client, error) {
	c := &Client{
		http:    &http.Client{Timeout: 20 * time.Second},
		token:   token,
		device:  RandomString(16),
		limiter: noopLimiter{},
		log:     slog.Default(),
	}
	for _, o := range opts {
		o(c)
	}
	if err := c.fetchUserInfo(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// UserID / Nickname are populated after construction.
func (c *Client) UserID() int64       { return c.userID }
func (c *Client) Nickname() string    { return c.nick }
func (c *Client) DeviceToken() string { return c.device }

// Token reports the credential this client authenticates with.
func (c *Client) Token() string { return c.token }

// generateHeaders builds the baseline device headers every platform endpoint
// expects, including the anonymous auth endpoints (SMS login flow).
func generateHeaders(device string) http.Header {
	h := http.Header{}
	h.Set("content-type", "application/json; charset=utf-8")
	h.Set("user-agent", "okhttp/3.14.9")
	h.Set("App-Version", "5.28.3")
	h.Set("AppType", "4")
	h.Set("deviceType", "1")
	h.Set("package-type", "uuyp")
	h.Set("DeviceToken", device)
	h.Set("DeviceId", device)
	h.Set("platform", "android")
	h.Set("Gameid", "730")
	// NOTE: do NOT set accept-encoding manually — a hand-set header disables
	// net/http's transparent gzip decompression and raw gzip reaches the JSON
	// decoder (observed 2026-08-23: SmsSignIn body starting with \x1f).
	devInfo, _ := json.Marshal(map[string]any{
		"deviceId": device, "deviceType": device, "hasSteamApp": 1,
		"requestTag":  strings.ToUpper(RandomString(32)),
		"systemName ": "Android", "systemVersion": "15",
	})
	h.Set("Device-Info", string(devInfo))
	return h
}

func (c *Client) headers(uk string) http.Header {
	h := generateHeaders(c.device)
	h.Set("uk", uk)
	h.Set("authorization", "Bearer "+c.token)
	return h
}

// loginHeaders is the header set for the anonymous auth endpoints. The
// reference client's generate_headers ALWAYS carries a uk header (random
// 65-char when uk_verify is off) — including the SMS login flow. Omitting uk
// there is a fingerprint deviation on exactly the endpoints risk control
// watches: every panel-driven SendSignInSmsCode observed so far was answered
// with 需进行图形校验 (audit 2026-08-25/27).
func loginHeaders(device string) http.Header {
	h := generateHeaders(device)
	h.Set("uk", RandomString(65))
	return h
}

func (c *Client) do(ctx context.Context, method, path string, payload any) ([]byte, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("uu: limiter: %w", err)
	}
	var body io.Reader
	if payload != nil && method != http.MethodGet {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("uu: marshal: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, body)
	if err != nil {
		return nil, err
	}
	req.Header = c.headers(RandomString(65))
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("uu: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("uu: read body: %w", err)
	}
	if resp.StatusCode == http.StatusMethodNotAllowed {
		return nil, ErrUKExpired
	}
	if resp.StatusCode != http.StatusOK || !isJSON(data) {
		return nil, fmt.Errorf("uu: %s %s: http %d non-json response", method, path, resp.StatusCode)
	}
	return data, nil
}

// envelope normalizes the platform's inconsistent Code/code casing.
type envelope struct {
	Code int             `json:"-"`
	Msg  string          `json:"-"`
	Data json.RawMessage `json:"-"`
}

func decodeEnvelope(data []byte) (*envelope, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("uu: decode envelope: %w", err)
	}
	e := &envelope{}
	v, ok := rawInt(m, "Code", "code")
	if !ok {
		return nil, fmt.Errorf("uu: decode envelope: missing Code/code")
	}
	e.Code = v
	e.Msg = rawString(m, "Msg", "msg")
	e.Data = m["Data"]
	if e.Data == nil {
		e.Data = m["data"]
	}
	return e, nil
}

func rawInt(m map[string]json.RawMessage, keys ...string) (int, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			var i int
			if json.Unmarshal(v, &i) == nil {
				return i, true
			}
		}
	}
	return 0, false
}

func rawString(m map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil {
				return s
			}
		}
	}
	return ""
}

func isJSON(b []byte) bool {
	t := bytes.TrimSpace(b)
	return len(t) > 0 && (t[0] == '{' || t[0] == '[')
}

// checkEnv translates envelope errors to unified sentinel errors.
func checkEnv(e *envelope, path string) error {
	switch e.Code {
	case codeOK:
		return nil
	case codeAuthExpired:
		return platform.ErrAuthExpired
	case codeRiskControl:
		return fmt.Errorf("%w at %s", platform.ErrPlatformBlocked, path)
	case codeVersionBlocked:
		// 5050 版本/注册门禁（api-notes §认证域）：第三方短信登录被拦。
		// 调度按 generic 冷却 + 审计，不设专用退避。
		return fmt.Errorf("%w at %s msg=%s", platform.ErrVersionBlocked, path, e.Msg)
	case codeCaptchaRequired:
		// 1110205 图形校验挑战：调用方需走 TCaptcha 重试链。
		return fmt.Errorf("%w at %s msg=%s", platform.ErrCaptchaRequired, path, e.Msg)
	default:
		return fmt.Errorf("uu: %s code=%d msg=%s", path, e.Code, e.Msg)
	}
}
