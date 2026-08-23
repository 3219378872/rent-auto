package uu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/3219378872/rent-auto/backend/internal/platform"
)

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
func SendLoginSmsCode(ctx context.Context, hc *http.Client, phone, sessionID string) error {
	if hc == nil {
		hc = &http.Client{Timeout: defaultHTTPTimeout}
	}
	payload := map[string]any{"Area": 86, "Mobile": phone, "Sessionid": sessionID, "Code": ""}
	_, err := postJSON(ctx, hc, "https://api.youpin898.com/api/user/Auth/SendSignInSmsCode", payload, nil)
	return err
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
	body, err := postJSON(ctx, hc, url, payload, nil)
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
