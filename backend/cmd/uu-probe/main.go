package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/platform/uu"
)

func main() {
	crypt, err := uu.NewCryptWithKey([]byte(uu.RandomString(16)))
	if err != nil {
		fmt.Println("NewCrypt:", err)
		return
	}
	fmt.Printf("ascii aes key=%q\n", crypt.Key())
	payload := map[string]string{
		"encryptedData":   crypt.EncryptAES([]byte(`{"iud":"` + uuid4() + `"}`)),
		"encryptedAesKey": "",
	}
	if payload["encryptedAesKey"], err = crypt.EncryptedAESKey(); err != nil {
		fmt.Println("aeskey:", err)
		return
	}
	body, _ := json.Marshal(payload)
	var raw []byte
	for i := 1; i <= 3; i++ {
		req, _ := http.NewRequestWithContext(context.Background(), "POST", "https://api.youpin898.com/api/deviceW2", bytes.NewReader(body))
		req.Header.Set("content-type", "application/json; charset=utf-8")
		resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
		if err != nil {
			fmt.Println("ERR", err)
			return
		}
		raw, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		fmt.Printf("try%d status=%d len=%d\n", i, resp.StatusCode, len(raw))
		if len(raw) > 0 {
			break
		}
		time.Sleep(5 * time.Second)
	}
	plain, err := crypt.DecryptAES(string(bytes.TrimSpace(raw)))
	if err != nil {
		fmt.Println("decrypt:", err)
		return
	}
	fmt.Printf("plain=%s\n", plain)
	var uk struct {
		U string `json:"u"`
	}
	if json.Unmarshal(plain, &uk) != nil || uk.U == "" {
		fmt.Println("no uk in payload")
		return
	}
	fmt.Printf("real uk len=%d\n", len(uk.U))

	// 注册设备信息（真实 App 登录前步骤）
	device := uu.RandomString(16)
	di, _ := json.Marshal(map[string]any{
		"deviceId": device, "deviceType": device, "hasSteamApp": 1,
		"requestTag": uu.RandomString(32), "systemName ": "Android", "systemVersion": "15",
	})

	req2, _ := http.NewRequest("GET", "https://api.youpin898.com/api/common/ClientInfo/AndroidInfo", nil)
	req2.Header = appHeaders(device, uk.U, "5.48.0", string(di))
	q2 := req2.URL.Query()
	q2.Set("DeviceToken", device)
	q2.Set("Sessionid", device)
	req2.URL.RawQuery = q2.Encode()
	resp2, err := (&http.Client{Timeout: 15 * time.Second}).Do(req2)
	if err != nil {
		fmt.Println("AndroidInfo ERR", err)
		return
	}
	raw2, _ := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()
	fmt.Printf("AndroidInfo status=%d body=%s\n", resp2.StatusCode, string(raw2))
	time.Sleep(2 * time.Second)

	probeSendH(appHeaders(device, uk.U, "5.48.0", string(di)), "5.48.0+devreg")
}

func appHeaders(device, uk, ver, devInfo string) http.Header {
	h := http.Header{}
	h.Set("content-type", "application/json; charset=utf-8")
	h.Set("user-agent", "okhttp/3.14.9")
	h.Set("App-Version", ver)
	h.Set("AppType", "4")
	h.Set("deviceType", "1")
	h.Set("package-type", "uuyp")
	h.Set("DeviceToken", device)
	h.Set("DeviceId", device)
	h.Set("platform", "android")
	h.Set("Gameid", "730")
	h.Set("uk", uk)
	h.Set("Device-Info", devInfo)
	return h
}

func probeSendH(h http.Header, label string) {
	device := h.Get("DeviceToken")
	payload, _ := json.Marshal(map[string]any{"Area": 86, "Mobile": "13800000000", "Sessionid": device, "Code": ""})
	req, _ := http.NewRequest("POST", "https://api.youpin898.com/api/user/Auth/SendSignInSmsCode", bytes.NewReader(payload))
	req.Header = h
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		fmt.Println(label, "ERR", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	fmt.Printf("send %s -> %s\n", label, string(raw))
}

// uuid4 renders a canonical UUID v4 with dashes, matching the reference.
func uuid4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
