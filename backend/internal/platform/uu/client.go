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
	codeOK          = 0
	codeAuthExpired = 84101 // login state invalid
	codeRiskControl = 84104 // rate limited / risk control
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

func (c *Client) headers(uk string) http.Header {
	h := http.Header{}
	h.Set("uk", uk)
	h.Set("authorization", "Bearer "+c.token)
	h.Set("content-type", "application/json; charset=utf-8")
	h.Set("user-agent", "okhttp/3.14.9")
	h.Set("App-Version", "5.28.3")
	h.Set("AppType", "4")
	h.Set("deviceType", "1")
	h.Set("package-type", "uuyp")
	h.Set("DeviceToken", c.device)
	h.Set("DeviceId", c.device)
	h.Set("platform", "android")
	h.Set("accept-encoding", "gzip")
	h.Set("Gameid", "730")
	devInfo, _ := json.Marshal(map[string]any{
		"deviceId": c.device, "deviceType": c.device, "hasSteamApp": 1,
		"requestTag":  strings.ToUpper(RandomString(32)),
		"systemName ": "Android", "systemVersion": "15",
	})
	h.Set("Device-Info", string(devInfo))
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
	if v, ok := rawInt(m, "Code", "code"); ok {
		e.Code = v
	}
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
	default:
		return fmt.Errorf("uu: %s code=%d msg=%s", path, e.Code, e.Msg)
	}
}
