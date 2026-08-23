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
	SmsModeDownlink = "down" // platform sent the code to the phone
	SmsModeUplink   = "up"   // phone owner must send an SMS manually
)

// SmsCodeResult reports how the login code is delivered after SendSignInSmsCode.
type SmsCodeResult struct {
	Mode string // SmsModeDownlink or SmsModeUplink
	Msg  string // platform message
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
// sessionID must be reused in SmsSignIn. uk may be empty.
// The platform reply decides the delivery mode: a Msg containing 成功 means a
// downstream SMS was sent; any other success Msg means the number was switched
// to the uplink flow and GetSmsUpSignInConfig carries the manual instructions.
func SendLoginSmsCode(ctx context.Context, hc *http.Client, phone, sessionID string) (SmsCodeResult, error) {
	if hc == nil {
		hc = &http.Client{Timeout: defaultHTTPTimeout}
	}
	payload := map[string]any{"Area": 86, "Mobile": phone, "Sessionid": sessionID, "Code": ""}
	body, err := postJSON(ctx, hc, apiBase+"/api/user/Auth/SendSignInSmsCode", payload, generateHeaders(sessionID))
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
		// Risk control demands a captcha — not the uplink flow; surface it.
		return SmsCodeResult{Mode: "", Msg: env.Msg}, fmt.Errorf("uu: send sms code blocked by risk control: %s", env.Msg)
	}
	if env.Code != codeOK {
		return res, checkEnv(env, "/api/user/Auth/SendSignInSmsCode")
	}
	return res, nil
}

// GetSmsUpSignInConfig fetches the manual-SMS instructions for the uplink
// login flow: the user must send Content to Number from the login phone,
// then complete sign-in with an empty code (SmsUpSignIn path).
func GetSmsUpSignInConfig(ctx context.Context, hc *http.Client) (SmsUpConfig, error) {
	if hc == nil {
		hc = &http.Client{Timeout: defaultHTTPTimeout}
	}
	body, err := getJSON(ctx, hc, apiBase+"/api/user/Auth/GetSmsUpSignInConfig", generateHeaders(RandomString(16)))
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
func SmsSignIn(ctx context.Context, hc *http.Client, phone, code, sessionID string) (string, error) {
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
	body, err := postJSON(ctx, hc, url, payload, generateHeaders(sessionID))
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
