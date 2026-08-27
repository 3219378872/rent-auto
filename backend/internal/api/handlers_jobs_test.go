package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/3219378872/rent-auto/backend/internal/platform/uu"
)

// recordingChannels captures the arguments forwarded to the channel registry
// so tests can assert session/ticket pass-through without a live upstream.
type recordingChannels struct {
	called     bool
	gotPhone   string
	gotSession string
	gotCaptcha *uu.CaptchaResult
	setToken   string
}

func (f *recordingChannels) Health(context.Context) map[string]string { return map[string]string{} }

func (f *recordingChannels) SendLoginSmsCode(_ context.Context, phone, sessionID string, captcha *uu.CaptchaResult) (uu.SmsCodeResult, error) {
	f.called, f.gotPhone, f.gotSession, f.gotCaptcha = true, phone, sessionID, captcha
	return uu.SmsCodeResult{Mode: uu.SmsModeDownlink, Msg: "验证码发送成功"}, nil
}

func (f *recordingChannels) GetSmsUpSignInConfig(context.Context) (uu.SmsUpConfig, error) {
	return uu.SmsUpConfig{}, nil
}

func (f *recordingChannels) VerifyUUSms(context.Context, string, string, string, string) (string, error) {
	return "abcd1234", nil
}

func (f *recordingChannels) SetUUToken(_ context.Context, token string) error {
	f.setToken = token
	return nil
}

func (f *recordingChannels) SetECOCreds(context.Context, string, string, string) error {
	return nil
}

func loginToken(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := do(t, h, "POST", "/api/v1/auth/login", "", `{"username":"admin","password":"hunter2"}`)
	if rec.Code != 200 {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	var lr struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lr); err != nil || lr.Token == "" {
		t.Fatalf("no token: %v %s", err, rec.Body.String())
	}
	return lr.Token
}

// The captcha ticket chain is issued against the session that drew the
// challenge. Retrying without that session_id must be rejected before the
// platform client runs — silently minting a fresh session breaks the
// reqTicket correlation upstream and loops the panel back into 图形校验.
func TestUUSmsCaptchaRetryRequiresOriginalSession(t *testing.T) {
	rc := &recordingChannels{}
	s := newTestServer(t)
	s.Channels = rc
	h := s.Routes()
	token := loginToken(t, h)

	rec := do(t, h, "POST", "/api/v1/channels/uu/sms", token,
		`{"phone":"13800000000","captcha":{"ticket":"tk","randstr":"rs"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("captcha retry without session_id: want 400, got %d %s", rec.Code, rec.Body.String())
	}
	if rc.called {
		t.Fatal("platform client must not be reached with a broken ticket chain")
	}

	rec = do(t, h, "POST", "/api/v1/channels/uu/sms", token,
		`{"phone":"13800000000","session_id":"sess-A","captcha":{"ticket":"tk","randstr":"rs","req_ticket":"rt"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid captcha retry rejected: %d %s", rec.Code, rec.Body.String())
	}
	if !rc.called || rc.gotPhone != "13800000000" || rc.gotSession != "sess-A" ||
		rc.gotCaptcha == nil || rc.gotCaptcha.Ticket != "tk" || rc.gotCaptcha.Randstr != "rs" ||
		rc.gotCaptcha.ReqTicket != "rt" {
		t.Fatalf("captcha pass-through broken: called=%v phone=%q session=%q captcha=%+v",
			rc.called, rc.gotPhone, rc.gotSession, rc.gotCaptcha)
	}
}

// Plain first sends still auto-mint a session when none was provided.
func TestUUSmsFirstSendMintsSession(t *testing.T) {
	rc := &recordingChannels{}
	s := newTestServer(t)
	s.Channels = rc
	h := s.Routes()
	token := loginToken(t, h)

	rec := do(t, h, "POST", "/api/v1/channels/uu/sms", token, `{"phone":"13800000000"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("first send failed: %d %s", rec.Code, rec.Body.String())
	}
	if !rc.called || len(rc.gotSession) != 16 {
		t.Fatalf("session not minted/passed: called=%v session=%q", rc.called, rc.gotSession)
	}
	var resp struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.SessionID != rc.gotSession {
		t.Fatalf("response session mismatch: %v %s vs %q", err, rec.Body.String(), rc.gotSession)
	}
}

// Manual token import: the 5050 platform gate blocks third-party SMS login
// entirely, so the operator pastes a token obtained from the official site.
// The handler must forward it to Channels.SetUUToken (validate+seal+rebuild).
func TestUUTokenImport(t *testing.T) {
	rc := &recordingChannels{}
	s := newTestServer(t)
	s.Channels = rc
	h := s.Routes()
	token := loginToken(t, h)

	rec := do(t, h, "PUT", "/api/v1/channels/uu", token, `{"token":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty token: want 400, got %d %s", rec.Code, rec.Body.String())
	}
	if rc.setToken != "" {
		t.Fatal("empty token must not reach the channel registry")
	}

	rec = do(t, h, "PUT", "/api/v1/channels/uu", token, `{"token":"jwt-abc.def.ghi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("import rejected: %d %s", rec.Code, rec.Body.String())
	}
	if rc.setToken != "jwt-abc.def.ghi" {
		t.Fatalf("token not forwarded: %q", rc.setToken)
	}
}
