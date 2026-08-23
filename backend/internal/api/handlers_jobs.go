package api

import (
	"context"
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
	Phone string `json:"phone"`
}

type uuSmsResponse struct {
	SessionID    string `json:"session_id"`
	Mode         string `json:"mode"`
	Msg          string `json:"msg,omitempty"`
	SmsUpContent string `json:"sms_up_content,omitempty"`
	SmsUpNumber  string `json:"sms_up_number,omitempty"`
}

type uuVerifyRequest struct {
	Phone     string `json:"phone"`
	Code      string `json:"code"`
	SessionID string `json:"session_id"`
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
	writeJSON(w, http.StatusOK, s.Channels.Health(r.Context()))
}

func (s *Server) handleUUSms(w http.ResponseWriter, r *http.Request) {
	var req uuSmsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Phone == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "phone required")
		return
	}
	session := domain.RandomSessionID()
	res, err := s.Channels.SendLoginSmsCode(r.Context(), req.Phone, session)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "sms_failed", err.Error())
		return
	}
	s.audit(r, "channel.uu.sms_sent", map[string]any{"mode": res.Mode, "msg": res.Msg})
	out := uuSmsResponse{SessionID: session, Mode: res.Mode, Msg: res.Msg}
	if res.Mode == uu.SmsModeUplink {
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
	if err := s.Channels.VerifyUUSms(r.Context(), req.Phone, req.Code, req.SessionID); err != nil {
		s.audit(r, "channel.uu.login_failed", map[string]any{"error": err.Error()})
		writeErr(w, http.StatusUnauthorized, "verify_failed", err.Error())
		return
	}
	s.audit(r, "channel.uu.login", map[string]any{})
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
	s.audit(r, "channel.eco.creds_update", map[string]any{})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

var _ = domain.ChannelUU
