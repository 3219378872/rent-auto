package eco

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
)

// testKeyPair generates an in-memory RSA pair (PEM, PKCS8 private / PKIX public).
func testKeyPair(t *testing.T) (privPEM, pubPEM []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	privPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	return
}

// newTestClient wires a client to a mock server; the handler receives the
// parsed body plus must answer with the raw response body.
func newTestClient(t *testing.T, handler func(t *testing.T, r *http.Request, body map[string]any) string) (*Client, *httptest.Server) {
	t.Helper()
	priv, _ := testKeyPair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := decodeBody(r, &body); err != nil {
			t.Errorf("bad body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(handler(t, r, body)))
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient("pid123", priv, WithBase(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func mustClient(t *testing.T, privPEM []byte, base string) *Client {
	t.Helper()
	c, err := NewClient("pid123", privPEM, WithBase(base))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
