package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestInitDataVerifier(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	verifier, err := NewInitDataVerifier("bot-token", time.Hour, time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	valid := signedInitData(map[string]string{
		"auth_date": "1790422200",
		"query_id":  "query",
		"user":      `{"id":67890,"first_name":"Max"}`,
	}, "bot-token")
	data, err := verifier.Verify(valid)
	if err != nil {
		t.Fatal(err)
	}
	if data.MaxUserID != "67890" || !data.AuthDate.Equal(now.Add(-30*time.Minute)) {
		t.Fatalf("unexpected init data: %+v", data)
	}

	tests := map[string]string{
		"tampered":       strings.Replace(valid, "query", "changed", 1),
		"duplicate hash": valid + "&hash=00",
		"missing user": signedInitData(map[string]string{
			"auth_date": "1790422200",
		}, "bot-token"),
		"malformed escape": "auth_date=1&user=%ZZ&hash=00",
		"old": signedInitData(map[string]string{
			"auth_date": "1790418599",
			"user":      `{"id":67890}`,
		}, "bot-token"),
		"future": signedInitData(map[string]string{
			"auth_date": "1790424061",
			"user":      `{"id":67890}`,
		}, "bot-token"),
		"zero user": signedInitData(map[string]string{
			"auth_date": "1790422200",
			"user":      `{"id":0}`,
		}, "bot-token"),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := verifier.Verify(raw); err != ErrInvalidInitData {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func signedInitData(values map[string]string, botToken string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values[key])
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secret.Write([]byte(botToken))
	signature := hmac.New(sha256.New, secret.Sum(nil))
	_, _ = signature.Write([]byte(strings.Join(parts, "\n")))

	encoded := make(url.Values, len(values)+1)
	for key, value := range values {
		encoded.Set(key, value)
	}
	encoded.Set("hash", hex.EncodeToString(signature.Sum(nil)))
	return encoded.Encode()
}
