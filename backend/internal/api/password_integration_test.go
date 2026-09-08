//go:build integration

package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/3219378872/rent-auto/backend/internal/auth"
)

func TestPasswordChangeRevokesSessionsAndOldPassword(t *testing.T) {
	st, cleanup := openAPIDB(t)
	defer cleanup()
	ctx := context.Background()
	old, err := auth.HashPassword("original-test-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSettingEnc(ctx, "admin_password_hash", []byte(old)); err != nil {
		t.Fatal(err)
	}
	s := NewServer(st, auth.NewJWT([]byte(testSecret)), "admin", "test", discardLogger())
	s.PasswordHash = st.AdminPasswordHash
	s.PasswordChange = st.ChangeAdminPassword
	h := s.Routes()
	login := func(password string, want int) string {
		t.Helper()
		body, _ := json.Marshal(loginRequest{Username: "admin", Password: password})
		r := do(t, h, "POST", "/api/v1/auth/login", "", string(body))
		if r.Code != want {
			t.Fatalf("login: %d want %d body=%s", r.Code, want, r.Body.String())
		}
		if want != 200 {
			return ""
		}
		var out loginResponse
		if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Token
	}
	token := login("original-test-password", 200)
	if r := do(t, h, "PUT", "/api/v1/auth/password", token, `{"current_password":"wrong","new_password":"replacement-test-password"}`); r.Code != 400 {
		t.Fatalf("bad current password: %d", r.Code)
	}
	if r := do(t, h, "PUT", "/api/v1/auth/password", token, `{"current_password":"original-test-password","new_password":"short"}`); r.Code != 400 {
		t.Fatalf("short password: %d", r.Code)
	}
	if r := do(t, h, "PUT", "/api/v1/auth/password", token, `{"current_password":"original-test-password","new_password":"replacement-test-password"}`); r.Code != 200 {
		t.Fatalf("change: %d %s", r.Code, r.Body.String())
	}
	if r := do(t, h, "GET", "/api/v1/auth/me", token, ""); r.Code != 401 {
		t.Fatalf("old token still live: %d", r.Code)
	}
	login("original-test-password", 401)
	fresh := login("replacement-test-password", 200)
	s.PasswordChange = nil
	if r := do(t, h, "PUT", "/api/v1/auth/password", fresh, `{"current_password":"replacement-test-password","new_password":"another-test-password"}`); r.Code != 409 {
		t.Fatalf("env-managed password: %d", r.Code)
	}
}
