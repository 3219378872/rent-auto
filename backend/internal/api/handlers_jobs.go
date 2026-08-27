package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform/uu"
)

type JobStatus struct {
	Name      string     `json:"name"`
	NextRun   time.Time  `json:"next_run"`
	LastRun   *time.Time `json:"last_run,omitempty"`
	LastOK    bool       `json:"last_ok"`
	LastError string     `json:"last_error,omitempty"`
	Running   bool       `json:"running"`
}

type JobController interface {
	StatusList() []JobStatus
	Trigger(ctx context.Context, name string) error
}

func (s *Server) handleJobsList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Jobs.StatusList())
}

func (s *Server) handleJobTrigger(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.Jobs.Trigger(r.Context(), name); err != nil {
		s.audit(r, "job.trigger.failed", map[string]any{"job": name, "error": err.Error()})
		writeErr(w, http.StatusBadRequest, "trigger_failed", err.Error())
		return
	}
	s.audit(r, "job.trigger", map[string]any{"job": name})
	writeJSON(w, http.StatusOK, map[string]string{"status": "triggered"})
}

// ---- channels ----

type uuSmsRequest struct {
	Phone     string          `json:"phone"`
	SessionID string          `json:"session_id,omitempty"`
	Captcha   *uuCaptchaInput `json:"captcha,omitempty"`
}

// uuCaptchaInput is the panel frontend's Tencent TCaptcha outcome, produced by
// the user completing the slider manually.
type uuCaptchaInput struct {
	Ticket    string `json:"ticket"`
	Randstr   string `json:"randstr"`
	ReqTicket string `json:"req_ticket"`
}

type uuSmsResponse struct {
	SessionID    string `json:"session_id"`
	Mode         string `json:"mode"`
	Msg          string `json:"msg,omitempty"`
	SmsUpContent string `json:"sms_up_content,omitempty"`
	SmsUpNumber  string `json:"sms_up_number,omitempty"`
	ReqTicket    string `json:"req_ticket,omitempty"` // captcha mode: echo back on retry
	Secs         int    `json:"secs,omitempty"`       // server-side cooldown seconds
}

type uuVerifyRequest struct {
	Phone          string `json:"phone"`
	Code           string `json:"code"`
	SessionID      string `json:"session_id"`
	LoginReqTicket string `json:"login_req_ticket,omitempty"`
}

type ecoCredsRequest struct {
	PartnerID     string `json:"partner_id"`
	PrivateKeyPEM string `json:"private_key_pem"`
	SteamID       string `json:"steam_id"`
}

func (s *Server) handleChannelsStatus(w http.ResponseWriter, r *http.Request) {
	if s.Channels == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "channels not initialized")
		return
	}
	out := s.Channels.Health(r.Context())
	if out == nil {
		out = map[string]string{}
	}
	if s.Steam != nil {
		out["steam"] = s.Steam.Health(r.Context())
	}
	writeJSON(w, http.StatusOK, out)
}

type steamCredsRequest struct {
	Username       string `json:"username"`
	Password       string `json:"password"`
	SharedSecret   string `json:"shared_secret"`
	IdentitySecret string `json:"identity_secret"`
}

func (s *Server) handleSteamCreds(w http.ResponseWriter, r *http.Request) {
	var req steamCredsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		req.Username == "" || req.Password == "" || req.SharedSecret == "" || req.IdentitySecret == "" {
		writeErr(w, http.StatusBadRequest, "bad_request",
			"username/password/shared_secret/identity_secret all required")
		return
	}
	if err := s.Steam.SetCredentials(r.Context(),
		req.Username, req.Password, req.SharedSecret, req.IdentitySecret); err != nil {
		s.audit(r, "channel.steam.creds_failed", map[string]any{"error": err.Error()})
		writeErr(w, http.StatusBadRequest, "login_failed", err.Error())
		return
	}
	// security-spec：凭证变更审计带指纹（Steam 凭证展示规则：不可见——以
	// SHA-256 前 12 位指纹关联，密钥本体不落审计）。
	s.audit(r, "channel.steam.creds_update", map[string]any{
		"username": req.Username, "secret_fp": credFingerprint(req.SharedSecret),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleUUSms(w http.ResponseWriter, r *http.Request) {
	var req uuSmsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Phone == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "phone required")
		return
	}
	session := req.SessionID
	if session == "" {
		session = domain.RandomSessionID()
	}
	var cv *uu.CaptchaResult
	if req.Captcha != nil && req.Captcha.Ticket != "" {
		if req.SessionID == "" {
			// The captcha ticket chain (ticket/reqTicket) is issued against the
			// session that drew the challenge. Minting a fresh session here
			// silently breaks that correlation upstream and loops the panel
			// back into 图形校验 forever — reject instead.
			writeErr(w, http.StatusBadRequest, "bad_request",
				"captcha retry must carry the session_id that received req_ticket")
			return
		}
		cv = &uu.CaptchaResult{
			Ticket: req.Captcha.Ticket, Randstr: req.Captcha.Randstr, ReqTicket: req.Captcha.ReqTicket,
		}
	}
	res, err := s.Channels.SendLoginSmsCode(r.Context(), req.Phone, session, cv)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "sms_failed", err.Error())
		return
	}
	s.audit(r, "channel.uu.sms_sent", map[string]any{"mode": res.Mode, "msg": res.Msg})
	out := uuSmsResponse{SessionID: session, Mode: res.Mode, Msg: res.Msg,
		ReqTicket: res.ReqTicket, Secs: res.Secs}
	switch res.Mode {
	case uu.SmsModeCaptcha:
		// VerifyRaw answers api-notes 待办①② (app-gateway Data shape) from the
		// wire instead of guessing — contents are ticket+secs, no PII.
		s.audit(r, "channel.uu.captcha_required", map[string]any{"msg": res.Msg, "verify_data": res.VerifyRaw})
	case uu.SmsModeUplink:
		cfg, err := s.Channels.GetSmsUpSignInConfig(r.Context())
		if err != nil {
			writeErr(w, http.StatusBadGateway, "sms_up_config_failed", err.Error())
			return
		}
		out.SmsUpContent, out.SmsUpNumber = cfg.Content, cfg.Number
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleUUSmsVerify(w http.ResponseWriter, r *http.Request) {
	var req uuVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid payload")
		return
	}
	tail, err := s.Channels.VerifyUUSms(r.Context(), req.Phone, req.Code, req.SessionID, req.LoginReqTicket)
	if err != nil {
		s.audit(r, "channel.uu.login_failed", map[string]any{"error": err.Error()})
		// 400, not 401: this is an upstream code rejection, NOT a panel-session
		// expiry — the frontend force-logouts on 401.
		writeErr(w, http.StatusBadRequest, "verify_failed", err.Error())
		return
	}
	// security-spec：凭证变更必须产生带尾号指纹的审计（UU token 规则：尾 8 位；
	// 手机号仅记尾 4 位）。凭证本体绝不入审计。
	s.audit(r, "channel.uu.login", map[string]any{
		"phone_tail": phoneTail(req.Phone, 4), "token_tail": tail,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleECOCreds(w http.ResponseWriter, r *http.Request) {
	var req ecoCredsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PartnerID == "" || req.PrivateKeyPEM == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "partner_id and private_key_pem required")
		return
	}
	if err := s.Channels.SetECOCreds(r.Context(), req.PartnerID, req.PrivateKeyPEM, req.SteamID); err != nil {
		s.audit(r, "channel.eco.creds_failed", map[string]any{"error": err.Error()})
		writeErr(w, http.StatusBadRequest, "invalid_creds", err.Error())
		return
	}
	// security-spec：ECO 私钥展示规则 = SHA-256 指纹前 12 位；partnerId 明文。
	s.audit(r, "channel.eco.creds_update", map[string]any{
		"partner_id": req.PartnerID, "key_fp": credFingerprint(req.PrivateKeyPEM),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// credFingerprint returns the first 12 hex chars of SHA-256(v) — enough to
// correlate credential updates without exposing the secret itself.
func credFingerprint(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])[:12]
}

// phoneTail returns the last n chars of a phone number ("" when shorter).
func phoneTail(s string, n int) string {
	if len(s) <= n {
		return ""
	}
	return s[len(s)-n:]
}
