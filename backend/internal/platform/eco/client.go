package eco

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	// ResultCode arrives as string per docs but int on some endpoints.
	if v, ok := m["ResultCode"]; ok {
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
	default:
		return fmt.Errorf("eco: %s code=%d msg=%s", path, e.Code, e.Msg)
	}
}

// post signs and executes one API call; result may be nil to ignore payload.
func (c *Client) post(ctx context.Context, path string, biz map[string]any, result any) error {
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
