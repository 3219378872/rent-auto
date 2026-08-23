package steam

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
)

// GenerateOneTimeCode ports guard.generate_one_time_code: the 30-second
// Steam Guard code from shared_secret. Deterministic given ts (tests).
func GenerateOneTimeCode(sharedSecretB64 string, ts int64) (string, error) {
	key, err := base64.StdEncoding.DecodeString(sharedSecretB64)
	if err != nil {
		return "", fmt.Errorf("steam: shared secret: %w", err)
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(ts/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	digest := mac.Sum(nil)
	begin := int(digest[19] & 0xF)
	full := binary.BigEndian.Uint32(digest[begin:begin+4]) & 0x7FFFFFFF
	const chars = "23456789BCDFGHJKMNPQRTVWXY"
	code := make([]byte, 5)
	for i := range code { // low bits first, mirroring upstream divmod loop
		var rem uint32
		full, rem = full/uint32(len(chars)), full%uint32(len(chars))
		code[i] = chars[rem]
	}
	return string(code), nil
}

// GenerateConfirmationKey ports guard.generate_confirmation_key:
// HMAC-SHA1(identity_secret, u64be(ts)+tag), base64 — signs mobileconf calls.
func GenerateConfirmationKey(identitySecretB64, tag string, ts int64) (string, error) {
	key, err := base64.StdEncoding.DecodeString(identitySecretB64)
	if err != nil {
		return "", fmt.Errorf("steam: identity secret: %w", err)
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(ts))
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	mac.Write([]byte(tag))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// GenerateDeviceID ports guard.generate_device_id.
func GenerateDeviceID(steamID string) string {
	sum := sha1Sum([]byte(steamID))
	hex := fmt.Sprintf("%x", sum)
	return "android:" + hex[0:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:32]
}
