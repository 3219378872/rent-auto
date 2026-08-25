package steam

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	storeURL     = "https://api.steampowered.com"
	communityURL = "https://steamcommunity.com"
	loginURL     = "https://login.steampowered.com"

	uaMobile = "Mozilla/5.0 (X11; Linux x86_64; rv:1.9.5.20) Gecko/2812-12-10 04:56:28 Firefox/3.8"
)

// Credentials are the four secrets required for a Steam session.
type Credentials struct {
	Username       string `json:"username"`
	Password       string `json:"password"`
	SharedSecret   string `json:"shared_secret"`   // base64, Steam Guard TOTP
	IdentitySecret string `json:"identity_secret"` // base64, mobile confirmations
}

// SessionTokens is the persisted auth state (stored encrypted at rest).
type SessionTokens struct {
	SteamID      string `json:"steamid"`
	RefreshToken string `json:"refresh_token"`
	AccessToken  string `json:"access_token"`
	AccessExp    int64  `json:"access_exp"`
}

// Session is an authenticated Steam web session.
type Session struct {
	http      *http.Client
	Creds     Credentials
	Tokens    SessionTokens
	sessionid string
}

// NewSession builds a session client with a fresh cookie jar.
func NewSession(creds Credentials) *Session {
	jar, _ := cookiejar.New(nil)
	return &Session{http: &http.Client{Timeout: 20 * time.Second, Jar: jar}, Creds: creds}
}

// AttachTokens restores a persisted token state into a new session and
// warms up cookies by visiting the community profile page.
func (s *Session) AttachTokens(tokens SessionTokens) {
	s.Tokens = tokens
	s.setLoginSecure()
	s.warmup()
}

func (s *Session) setLoginSecure() {
	val := url.QueryEscape(s.Tokens.SteamID + "||" + s.Tokens.AccessToken)
	for _, domain := range []string{"steamcommunity.com", "store.steampowered.com"} {
		s.http.Jar.SetCookies(mustURL("https://"+domain), []*http.Cookie{{
			Name: "steamLoginSecure", Value: val, Domain: domain, Path: "/",
		}})
	}
	if s.Tokens.RefreshToken != "" {
		s.http.Jar.SetCookies(mustURL("https://steamcommunity.com"), []*http.Cookie{{
			Name:   "steamRefresh_steam",
			Value:  url.QueryEscape(s.Tokens.SteamID + "||" + s.Tokens.RefreshToken),
			Domain: "steamcommunity.com", Path: "/",
		}})
	}
}

func (s *Session) warmup() {
	resp, err := s.http.Get(communityURL + "/my")
	if err == nil {
		defer func() { _ = resp.Body.Close() }()
		s.captureSessionID()
	}
}

func (s *Session) captureSessionID() {
	u := mustURL(communityURL)
	for _, c := range s.http.Jar.Cookies(u) {
		if c.Name == "sessionid" {
			s.sessionid = c.Value
		}
	}
}

// ---- low-level calls ----

// authAPIPOST posts protobuf input to IAuthenticationService and returns raw body.
func (s *Session) authAPIPOST(ctx context.Context, method string, msg []byte) ([]byte, error) {
	form := url.Values{"input_protobuf_encoded": {base64.StdEncoding.EncodeToString(msg)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		storeURL+"/IAuthenticationService/"+method+"/v1/",
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", uaMobile)
	return s.doRaw(req)
}

func (s *Session) doRaw(req *http.Request) ([]byte, error) {
	body, _, err := s.doRawStatus(req)
	return body, err
}

// doRawStatus is doRaw plus the final HTTP status code, for callers that must
// distinguish transport success from application-level failure (write ops).
func (s *Session) doRawStatus(req *http.Request) ([]byte, int, error) {
	//nolint:gosec // G704：URL 来自 Steam 登录响应的 transfer_info（上游固定域名），协议要求原样回放
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("steam: %s %s: %w", req.Method, req.URL.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound {
		if loc := resp.Header.Get("Location"); loc != "" {
			next, err := http.NewRequestWithContext(req.Context(), req.Method, loc, nil)
			if err != nil {
				return nil, resp.StatusCode, err
			}
			return s.doRawStatus(next)
		}
	}
	return body, resp.StatusCode, nil
}

// ---- login state machine ----

var ErrEmailCodeRequired = errors.New("steam: email-code guard not supported (use shared_secret)")

type beginResponse struct {
	ClientID             uint64
	RequestID            []byte
	SteamID              uint64
	AllowedConfirmations []int32 // EAuthSessionGuardType values
}

func encodeBeginAuth(user, encPassword string, ts uint64) []byte {
	var w pbWriter
	w.str(1, uaMobile)    // device_friendly_name
	w.str(2, user)        // account_name
	w.str(3, encPassword) // encrypted_password
	w.u64(4, ts)          // encryption_timestamp
	w.b32(5, true)        // remember_login
	w.u64(6, 3)           // platform_type = MobileApp
	w.u64(7, 1)           // persistence = Persistent
	w.str(8, "Community") // website_id
	return w.buf
}

func decodeBeginResponse(b []byte) (*beginResponse, error) {
	r := newPBReader(b)
	out := &beginResponse{}
	for {
		field, wire, payload, num, err := r.next()
		if err == errProtoDone {
			break
		}
		if err != nil {
			return nil, err
		}
		switch field {
		case 1:
			out.ClientID = num
		case 2:
			out.RequestID = payload
		case 5:
			out.SteamID = num
		case 4:
			if wire == 2 && len(payload) > 0 {
				cr := newPBReader(payload)
				for {
					f2, _, _, n2, err := cr.next()
					if err == errProtoDone {
						break
					}
					if err != nil {
						return nil, err
					}
					if f2 == 1 {
						out.AllowedConfirmations = append(out.AllowedConfirmations, int32(n2))
					}
				}
			}
		}
	}
	return out, nil
}

func encodePoll(clientID uint64, requestID []byte) []byte {
	var w pbWriter
	w.u64(1, clientID)
	w.bytes(2, requestID)
	return w.buf
}

func encodeUpdateGuard(clientID, steamID uint64, code string, codeType int32) []byte {
	var w pbWriter
	w.u64(1, clientID)
	w.u64(2, steamID)
	w.str(3, code)
	w.u64(4, uint64(codeType))
	return w.buf
}

// Login performs the full username/password flow and populates s.Tokens.
func (s *Session) Login(ctx context.Context) error {
	// warm cookies first (upstream GETs community before RSA fetch)
	if resp, err := s.http.Get(communityURL); err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		s.captureSessionID()
	}

	// 1. RSA public key
	rsaMsg := func() []byte {
		var w pbWriter
		w.str(1, s.Creds.Username)
		return w.buf
	}()
	body, err := s.authAPIGET(ctx, "GetPasswordRSAPublicKey", rsaMsg)
	if err != nil {
		return err
	}
	pubMod, pubExp, rsaTS, err := decodeRSAKey(body)
	if err != nil {
		return err
	}

	// 2. encrypt password
	encPwd, err := encryptPassword(s.Creds.Password, pubMod, pubExp)
	if err != nil {
		return err
	}

	// 3. begin auth session
	beginBody, err := s.authAPIPOST(ctx, "BeginAuthSessionViaCredentials",
		encodeBeginAuth(s.Creds.Username, encPwd, rsaTS))
	if err != nil {
		return err
	}
	begin, err := decodeBeginResponse(beginBody)
	if err != nil {
		return err
	}
	if len(begin.AllowedConfirmations) == 0 {
		return errors.New("steam: no allowed confirmations returned")
	}
	guardType := begin.AllowedConfirmations[0]

	// 4. satisfy guard
	switch guardType {
	case 3: // DeviceCode → shared_secret TOTP
		code, err := GenerateOneTimeCode(s.Creds.SharedSecret, time.Now().Unix())
		if err != nil {
			return err
		}
		if _, err := s.authAPIPOST(ctx, "UpdateAuthSessionWithSteamGuardCode",
			encodeUpdateGuard(begin.ClientID, begin.SteamID, code, 3)); err != nil {
			return err
		}
	case 4: // DeviceConfirmation → acknowledge with code_type=4, code "ok"
		if _, err := s.authAPIPOST(ctx, "UpdateAuthSessionWithSteamGuardCode",
			encodeUpdateGuard(begin.ClientID, begin.SteamID, "ok", 4)); err != nil {
			return err
		}
	default:
		return ErrEmailCodeRequired
	}

	// 5. poll tokens
	pollBody, err := s.authAPIPOST(ctx, "PollAuthSessionStatus",
		encodePoll(begin.ClientID, begin.RequestID))
	if err != nil {
		return err
	}
	refreshTok, accessTok, err := decodePoll(pollBody)
	if err != nil {
		return err
	}
	if refreshTok == "" {
		return errors.New("steam: empty refresh token after poll")
	}
	s.Tokens = SessionTokens{
		SteamID:      strconv.FormatUint(begin.SteamID, 10),
		RefreshToken: refreshTok, AccessToken: accessTok,
		AccessExp: jwtExp(accessTok),
	}
	return s.finalize(ctx)
}

func (s *Session) authAPIGET(ctx context.Context, method string, msg []byte) ([]byte, error) {
	form := url.Values{"input_protobuf_encoded": {base64.StdEncoding.EncodeToString(msg)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		storeURL+"/IAuthenticationService/"+method+"/v1/?"+form.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", uaMobile)
	return s.doRaw(req)
}

func decodeRSAKey(body []byte) (mod, exp string, ts uint64, err error) {
	r := newPBReader(body)
	for {
		field, wire, payload, num, e := r.next()
		if e == errProtoDone {
			break
		}
		if e != nil {
			return "", "", 0, e
		}
		switch field {
		case 1:
			mod = string(payload)
		case 2:
			exp = string(payload)
		case 3:
			if wire == 0 {
				ts = num
			}
		}
	}
	if mod == "" || exp == "" || ts == 0 {
		return "", "", 0, errors.New("steam: incomplete RSA params")
	}
	return mod, exp, ts, nil
}

func encryptPassword(password, modHex, expHex string) (string, error) {
	mod, ok := new(big.Int).SetString(modHex, 16)
	if !ok || mod.Sign() <= 0 {
		return "", errors.New("steam: bad rsa modulus")
	}
	exp, ok := new(big.Int).SetString(expHex, 16)
	if !ok || exp.Sign() <= 0 || !exp.IsInt64() {
		return "", errors.New("steam: bad rsa exponent")
	}
	pub := &rsa.PublicKey{N: mod, E: int(exp.Int64())}
	enc, err := rsa.EncryptPKCS1v15(rand.Reader, pub, []byte(password)) //nolint:staticcheck // 平台线协议强制 PKCS#1 v1.5，服务端固定无法换 OAEP
	if err != nil {
		return "", fmt.Errorf("steam: rsa encrypt: %w", err)
	}
	return base64.StdEncoding.EncodeToString(enc), nil
}

func decodePoll(body []byte) (refresh, access string, err error) {
	r := newPBReader(body)
	for {
		field, wire, payload, _, e := r.next()
		if e == errProtoDone {
			break
		}
		if e != nil {
			return "", "", e
		}
		if wire == 2 {
			switch field {
			case 3:
				refresh = string(payload)
			case 4:
				access = string(payload)
			}
		}
	}
	return refresh, access, nil
}

// finalize exchanges the refresh token for community cookies.
func (s *Session) finalize(ctx context.Context) error {
	form := url.Values{
		"nonce":     {s.Tokens.RefreshToken},
		"sessionid": {s.sessionid},
		"redir":     {communityURL + "/login/home/?goto="},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		loginURL+"/jwt/finalizelogin", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", communityURL+"/")
	req.Header.Set("Origin", communityURL)
	body, err := s.doRaw(req)
	if err != nil {
		return err
	}
	var fin struct {
		TransferInfo []struct {
			URL    string `json:"url"`
			Params struct {
				Nonce string `json:"nonce"`
				Auth  string `json:"auth"`
			} `json:"params"`
		} `json:"transfer_info"`
		SteamID interface{} `json:"steamID"`
	}
	if err := json.Unmarshal(body, &fin); err != nil {
		return fmt.Errorf("steam: finalizelogin decode: %w", err)
	}
	for _, t := range fin.TransferInfo {
		tform := url.Values{"steamID": {s.Tokens.SteamID}, "auth": {t.Params.Auth}, "nonce": {t.Params.Nonce}}
		tr, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, strings.NewReader(tform.Encode()))
		if err != nil {
			return err
		}
		tr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if _, err := s.doRaw(tr); err != nil {
			return err
		}
	}
	s.setLoginSecure()
	s.warmup()

	// acknowledge new-device trade notice (upstream does this post-login)
	ack := url.Values{"sessionid": {s.sessionid}, "message": {"1"}}
	ar, err := http.NewRequestWithContext(ctx, http.MethodPost,
		communityURL+"/trade/new/acknowledge", strings.NewReader(ack.Encode()))
	if err == nil {
		ar.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		ar.Header.Set("Referer", communityURL+"/trade/new")
		if resp, err := s.http.Do(ar); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}
	return nil
}

// RefreshAccessToken rotates access_token using the refresh_token.
func (s *Session) RefreshAccessToken(ctx context.Context) error {
	if s.Tokens.RefreshToken == "" || s.Tokens.SteamID == "" {
		return errors.New("steam: no refresh token")
	}
	form := url.Values{"steamid": {s.Tokens.SteamID}, "refresh_token": {s.Tokens.RefreshToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		storeURL+"/IAuthenticationService/GenerateAccessTokenForApp/v1/",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var parsed struct {
		Response struct {
			AccessToken string `json:"access_token"`
		} `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return err
	}
	if parsed.Response.AccessToken == "" {
		return errors.New("steam: refresh returned empty access token")
	}
	s.Tokens.AccessToken = parsed.Response.AccessToken
	s.Tokens.AccessExp = jwtExp(parsed.Response.AccessToken)
	s.setLoginSecure()
	return nil
}

// IsAlive checks whether steamLoginSecure still yields an authorized page.
func (s *Session) IsAlive(ctx context.Context) bool {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, communityURL+"/my", nil)
	resp, err := s.http.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	s.captureSessionID()
	return resp.StatusCode == http.StatusOK
}

// jwtExp extracts the exp claim from a JWT without signature verification
// (tokens come from Steam over TLS; we only read expiry for scheduling).
func jwtExp(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return 0
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return 0
	}
	return claims.Exp
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
