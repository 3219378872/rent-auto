package eco

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/platform"
)

const defaultBase = "https://openapi.ecosteam.cn"

// Result codes from official docs (platform-eco-api-notes.md).
const (
	codeOK          = 0
	codeRateLimited = 6001
	codeIPWhitelist = 4004 // credential/env-class: merchant IP not whitelisted
	codeIdentityID  = 5005 // credential-class: identity id invalid
	codeMissingSid  = 2001 // deterministic caller bug: SteamId empty
	codeBadTime     = 5003 // timestamp invalid/expired (clock skew or replay)
	codeWindowLimit = 7002 // query window exceeds 31 days (segmented away by SellerRentOrderList)
)

// Client is the ECOSteam open API client.
type Client struct {
	http       *http.Client
	base       string
	partnerID  string
	privateKey []byte
	limiter    platform.Limiter
}

type Option func(*Client)

func WithHTTP(h *http.Client) Option        { return func(c *Client) { c.http = h } }
func WithBase(b string) Option              { return func(c *Client) { c.base = b } }
func WithLimiter(l platform.Limiter) Option { return func(c *Client) { c.limiter = l } }

func NewClient(partnerID string, privateKeyPEM []byte, opts ...Option) (*Client, error) {
	if partnerID == "" {
		return nil, fmt.Errorf("eco: partner id required")
	}
	c := &Client{
		http:       &http.Client{Timeout: 20 * time.Second},
		base:       defaultBase,
		partnerID:  partnerID,
		privateKey: privateKeyPEM,
		limiter:    noopLim{},
	}
	for _, o := range opts {
		o(c)
	}
	if _, err := ParsePrivateKey(privateKeyPEM); err != nil {
		return nil, err
	}
	return c, nil
}

type noopLim struct{}

func (noopLim) Wait(context.Context) error { return nil }

type envelope struct {
	Code int             `json:"-"`
	Msg  string          `json:"-"`
	Data json.RawMessage `json:"-"`
}

func decodeEnvelope(body []byte) (*envelope, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("eco: decode envelope: %w", err)
	}
	e := &envelope{}
	// Fail closed: an envelope without ResultCode must not decode as code=0
	// (= success). Proxy/gateway error bodies can be valid JSON without it.
	v, ok := m["ResultCode"]
	if !ok {
		return nil, fmt.Errorf("eco: decode envelope: missing ResultCode")
	}
	// ResultCode arrives as string per docs but int on some endpoints.
	{
		var s string
		if json.Unmarshal(v, &s) == nil {
			n, _ := strconv.Atoi(s)
			e.Code = n
		} else {
			_ = json.Unmarshal(v, &e.Code)
		}
	}
	if v, ok := m["ResultMsg"]; ok {
		_ = json.Unmarshal(v, &e.Msg)
	}
	e.Data = m["ResultData"]
	return e, nil
}

func (c *Client) checkEnv(e *envelope, path string) error {
	switch e.Code {
	case codeOK:
		return nil
	case codeRateLimited:
		return fmt.Errorf("%w at %s", platform.ErrRateLimited, path)
	case codeIPWhitelist, codeIdentityID, codeBadTime:
		// Credential/env-class failures: without a unified sentinel the scheduler's
		// risk-control cooldown never engages and every cycle retries blind.
		// 5003 counts here: a bad timestamp is a signing-environment fault
		// (clock skew), not a per-request bug.
		return fmt.Errorf("%w at %s code=%d msg=%s", platform.ErrAuthExpired, path, e.Code, e.Msg)
	case codeWindowLimit:
		// 7002 31天窗上限：SellerRentOrderList 已按 30 天分段，正常流程永不
		// 触发；一旦出现说明调用方误用，回退为限流哨兵触发调度冷却 + 审计。
		return fmt.Errorf("%w at %s code=%d msg=%s", platform.ErrRateLimited, path, e.Code, e.Msg)
	case codeMissingSid:
		// 2001 系确定性调用方 bug（SteamId 缺失）：重试/冷却永无帮助，
		// 故意不映射哨兵，避免喂给风控退避通道。
		return fmt.Errorf("eco: %s code=%d msg=%s", path, e.Code, e.Msg)
	default:
		return fmt.Errorf("eco: %s code=%d msg=%s", path, e.Code, e.Msg)
	}
}

// Rate-limit retry policy (api-notes §限频: 6001 → 指数退避，最多 3 次).
const rateRetryAttempts = 3

var rateRetryBase = 500 * time.Millisecond

// post signs and executes one API call; result may be nil to ignore payload.
// A 6001 (rate limited) response is retried with exponential backoff plus
// jitter: without it, every throttled worker retries on the same tick and
// re-triggers 6001 in lockstep (thundering herd).
func (c *Client) post(ctx context.Context, path string, biz map[string]any, result any) error {
	for attempt := 0; ; attempt++ {
		err := c.postOnce(ctx, path, biz, result)
		if attempt < rateRetryAttempts-1 && errors.Is(err, platform.ErrRateLimited) {
			delay := rateRetryBase<<attempt + time.Duration(rand.Int63n(int64(rateRetryBase)))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			continue
		}
		return err
	}
}

func (c *Client) postOnce(ctx context.Context, path string, biz map[string]any, result any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("eco: limiter: %w", err)
	}
	params := make(map[string]any, len(biz)+2)
	for k, v := range biz {
		params[k] = v
	}
	params["PartnerId"] = c.partnerID
	params["Timestamp"] = strconv.FormatInt(time.Now().Unix(), 10)

	canonical := SignString(params)
	sig, err := Sign(c.privateKey, canonical)
	if err != nil {
		return err
	}
	params["Sign"] = sig

	body, err := marshalBody(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("eco: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("eco: read body: %w", err)
	}
	// Fail closed on transport status: a 5xx/3xx body may still be JSON but is
	// not an API answer; treating it as success faked write operations
	// (e.g. ghost delists) in the pre-fix behavior.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("eco: %s http status %d", path, resp.StatusCode)
	}
	env, err := decodeEnvelope(data)
	if err != nil {
		return err
	}
	if err := c.checkEnv(env, path); err != nil {
		return err
	}
	if result != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, result); err != nil {
			return fmt.Errorf("eco: %s payload: %w", path, err)
		}
	}
	return nil
}

// postRaw returns the raw ResultData payload.
func (c *Client) postRaw(ctx context.Context, path string, biz map[string]any) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.post(ctx, path, biz, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// marshalBody renders params as JSON with scalars in their canonical literal form.
func marshalBody(params map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	for k, v := range orderedParams(params) {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		if s, ok := v.(string); ok {
			sb, _ := json.Marshal(s)
			buf.Write(sb)
		} else {
			buf.WriteString(CompactJSON(v))
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// orderedParams returns keys sorted for deterministic bodies (tests/debug).
func orderedParams(params map[string]any) map[string]any { return params }
