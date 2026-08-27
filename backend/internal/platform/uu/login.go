package uu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/3219378872/rent-auto/backend/internal/platform"
)

// SMS code delivery modes reported by SendSignInSmsCode.
const (
	SmsModeDownlink = "down"    // platform sent the code to the phone
	SmsModeUplink   = "up"      // phone owner must send an SMS manually
	SmsModeCaptcha  = "captcha" // risk control demands a manual slider/click captcha
)

// SmsCodeResult reports how the login code is delivered after SendSignInSmsCode.
type SmsCodeResult struct {
	Mode           string // SmsModeDownlink, SmsModeUplink or SmsModeCaptcha
	Msg            string // platform message
	ReqTicket      string // captcha mode: correlation ticket issued by the blocked reply
	Secs           int    // cooldown seconds before the next attempt (0 = unknown)
	LoginReqTicket string // success: correlation ticket for the subsequent sign-in call
	// VerifyRaw is the raw Data of a captcha-blocked reply (ticket + secs only,
	// no PII). The app gateway's field casing is still 未真机校订 (api-notes
	// 待办①②); surfacing it lets the audit trail answer the shape question on
	// the next real attempt instead of guessing.
	VerifyRaw string
}

// CaptchaResult carries the Tencent TCaptcha outcome used to retry a
// captcha-blocked SendLoginSmsCode. Produced by the panel frontend after the
// user completes the slider manually; never synthesized server-side.
type CaptchaResult struct {
	Ticket    string
	Randstr   string
	ReqTicket string
}

// SmsUpConfig describes the manual SMS required by the uplink flow.
type SmsUpConfig struct {
	Content string
	Number  string
}

// GetUserInfo validates the token and fills UserID/Nickname.
func (c *Client) fetchUserInfo(ctx context.Context) error {
	data, err := c.do(ctx, "GET", "/api/user/Account/getUserInfo", nil)
	if err != nil {
		return err
	}
	env, err := decodeEnvelope(data)
	if err != nil {
		return err
	}
	if err := checkEnv(env, "/api/user/Account/getUserInfo"); err != nil {
		return err
	}
	var d struct {
		NickName string `json:"NickName"`
		UserID   int64  `json:"UserId"`
	}
	if err := json.Unmarshal(env.Data, &d); err != nil {
		return fmt.Errorf("uu: getuserinfo payload: %w", err)
	}
	if d.UserID == 0 {
		return platform.ErrAuthExpired
	}
	c.userID, c.nick = d.UserID, d.NickName
	return nil
}

// ---- SMS login flow (panel-driven; no interactive stdin) ----

// SendLoginSmsCode requests an SMS login code for phone (+86).
// sessionID must be reused in SmsSignIn and across captcha retries. uk may be
// empty. The platform reply decides the delivery mode: a Msg containing 成功
// means a downstream SMS was sent; any other success Msg means the number was
// switched to the uplink flow and GetSmsUpSignInConfig carries the manual
// instructions. A 需进行图形校验 reply yields Mode=SmsModeCaptcha (no error)
// together with ReqTicket/Secs — retry with a *CaptchaResult solved by the
// panel frontend (Tencent TCaptcha aid 191004049), never server-side.
func SendLoginSmsCode(ctx context.Context, hc *http.Client, phone, sessionID string, captcha *CaptchaResult) (SmsCodeResult, error) {
	if hc == nil {
		hc = &http.Client{Timeout: defaultHTTPTimeout}
	}
	payload := map[string]any{"Area": 86, "Mobile": phone, "Sessionid": sessionID, "Code": ""}
	if captcha != nil {
		payload["behaviorVerifyResult"] = map[string]string{
			"randstr":   captcha.Randstr,
			"ticket":    captcha.Ticket,
			"reqTicket": captcha.ReqTicket,
		}
	}
	body, err := postJSON(ctx, hc, apiBase+"/api/user/Auth/SendSignInSmsCode", payload, loginHeaders(sessionID))
	if err != nil {
		return SmsCodeResult{}, err
	}
	env, err := decodeEnvelope(body)
	if err != nil {
		return SmsCodeResult{}, err
	}
	res := SmsCodeResult{Mode: SmsModeUplink, Msg: env.Msg}
	switch {
	case strings.Contains(env.Msg, "成功"):
		res.Mode = SmsModeDownlink
	case strings.Contains(env.Msg, "图形校验"), strings.Contains(env.Msg, "滑块"):
		res.Mode = SmsModeCaptcha
		res.ReqTicket, res.Secs = parseVerifyData(env.Data)
		res.VerifyRaw = strings.TrimSpace(string(env.Data))
		return res, nil
	}
	if env.Code != codeOK {
		return res, checkEnv(env, "/api/user/Auth/SendSignInSmsCode")
	}
	if res.Mode == SmsModeDownlink {
		res.LoginReqTicket, res.Secs = parseVerifyData(env.Data)
	}
	return res, nil
}

// parseVerifyData extracts the correlation ticket and cooldown seconds from a
// SendSignInSmsCode Data payload. Field casing differs between the PC web
// gateway (camelCase, captured 2026-08-23) and the app gateway (unverified,
// api-notes 待办①②) — match keys case-insensitively and fall back to any
// *reqticket* key so the ticket chain never silently degrades to an empty
// reqTicket (an empty one makes the platform re-challenge forever).
func parseVerifyData(data json.RawMessage) (ticket string, secs int) {
	if len(data) == 0 {
		return "", 0
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return "", 0
	}
	lower := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		lower[strings.ToLower(k)] = v
	}
	ticket = rawString(lower, "behaviorverifyreqticket", "loginreqticket", "reqticket")
	if ticket == "" {
		for k, v := range lower {
			flat := strings.NewReplacer("_", "", "-", "").Replace(k)
			if strings.Contains(flat, "reqticket") {
				var s string
				if json.Unmarshal(v, &s) == nil && s != "" {
					ticket = s
					break
				}
			}
		}
	}
	secs, _ = rawInt(lower, "secs")
	return ticket, secs
}

// GetSmsUpSignInConfig fetches the manual-SMS instructions for the uplink
// login flow: the user must send Content to Number from the login phone,
// then complete sign-in with an empty code (SmsUpSignIn path).
func GetSmsUpSignInConfig(ctx context.Context, hc *http.Client) (SmsUpConfig, error) {
	if hc == nil {
		hc = &http.Client{Timeout: defaultHTTPTimeout}
	}
	body, err := getJSON(ctx, hc, apiBase+"/api/user/Auth/GetSmsUpSignInConfig", loginHeaders(RandomString(16)))
	if err != nil {
		return SmsUpConfig{}, err
	}
	env, err := decodeEnvelope(body)
	if err != nil {
		return SmsUpConfig{}, err
	}
	if err := checkEnv(env, "/api/user/Auth/GetSmsUpSignInConfig"); err != nil {
		return SmsUpConfig{}, err
	}
	var d struct {
		SmsUpContent string `json:"SmsUpContent"`
		SmsUpNumber  string `json:"SmsUpNumber"`
	}
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return SmsUpConfig{}, fmt.Errorf("uu: smsup config payload: %w", err)
		}
	}
	return SmsUpConfig{Content: d.SmsUpContent, Number: d.SmsUpNumber}, nil
}

// SmsSignIn exchanges the SMS code for a token. Empty code falls back to the
// SmsUp (SMS uplink) sign-in path used when the platform requires manual SMS.
// loginReqTicket carries the correlation ticket issued by a successful
// SendLoginSmsCode; pass "" when unknown.
func SmsSignIn(ctx context.Context, hc *http.Client, phone, code, sessionID, loginReqTicket string) (string, error) {
	if hc == nil {
		hc = &http.Client{Timeout: defaultHTTPTimeout}
	}
	url := "https://api.youpin898.com/api/user/Auth/SmsSignIn"
	if code == "" {
		url = "https://api.youpin898.com/api/user/Auth/SmsUpSignIn"
	}
	payload := map[string]any{
		"Area": 86, "Code": code, "DeviceName": sessionID,
		"Sessionid": sessionID, "Mobile": phone,
	}
	if loginReqTicket != "" {
		payload["loginReqTicket"] = loginReqTicket
	}
	body, err := postJSON(ctx, hc, url, payload, loginHeaders(sessionID))
	if err != nil {
		return "", err
	}
	var resp struct {
		Code int    `json:"Code"`
		Msg  string `json:"Msg"`
		Data struct {
			Token string `json:"Token"`
		} `json:"Data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("uu: sms signin decode: %w", err)
	}
	if resp.Code != 0 || resp.Data.Token == "" {
		return "", fmt.Errorf("uu: sms signin failed: %s", resp.Msg)
	}
	return resp.Data.Token, nil
}

// GetUUUK obtains the anti-bot "uk" parameter via the deviceW2 encrypted endpoint.
func GetUUUK(ctx context.Context, hc *http.Client) (string, error) {
	if hc == nil {
		hc = &http.Client{Timeout: defaultHTTPTimeout}
	}
	crypt, err := NewCrypt()
	if err != nil {
		return "", err
	}
	payload := map[string]string{
		"encryptedData":   crypt.EncryptAES([]byte(`{"iud":"` + RandomString(36) + `"}`)),
		"encryptedAesKey": "",
	}
	if payload["encryptedAesKey"], err = crypt.EncryptedAESKey(); err != nil {
		return "", err
	}
	body, err := postJSON(ctx, hc, "https://api.youpin898.com/api/deviceW2", payload, nil)
	if err != nil {
		return "", err
	}
	plain, err := crypt.DecryptAES(string(bytes.TrimSpace(body)))
	if err != nil {
		return "", err
	}
	var uk struct {
		U string `json:"u"`
	}
	if err := json.Unmarshal(plain, &uk); err != nil || uk.U == "" {
		return "", fmt.Errorf("uu: deviceW2 payload: %w", err)
	}
	return uk.U, nil
}
