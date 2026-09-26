package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrInvalidInitData = errors.New("invalid MAX init data")

type InitData struct {
	MaxUserID string
	AuthDate  time.Time
}

type InitDataVerifier struct {
	botToken  string
	maxAge    time.Duration
	futureGap time.Duration
	clock     func() time.Time
}

func NewInitDataVerifier(
	botToken string,
	maxAge time.Duration,
	futureGap time.Duration,
	clock func() time.Time,
) (*InitDataVerifier, error) {
	if botToken == "" {
		return nil, errors.New("MAX bot token is required")
	}
	if maxAge <= 0 || futureGap < 0 {
		return nil, errors.New("invalid init data lifetime")
	}
	if clock == nil {
		clock = time.Now
	}
	return &InitDataVerifier{botToken: botToken, maxAge: maxAge, futureGap: futureGap, clock: clock}, nil
}

func (v *InitDataVerifier) Verify(raw string) (InitData, error) {
	values, hash, dataCheck, err := parseInitData(raw)
	if err != nil {
		return InitData{}, ErrInvalidInitData
	}

	providedHash, err := hex.DecodeString(hash)
	if err != nil || len(providedHash) != sha256.Size {
		return InitData{}, ErrInvalidInitData
	}
	secretMAC := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secretMAC.Write([]byte(v.botToken))
	signatureMAC := hmac.New(sha256.New, secretMAC.Sum(nil))
	_, _ = signatureMAC.Write([]byte(dataCheck))
	if !hmac.Equal(signatureMAC.Sum(nil), providedHash) {
		return InitData{}, ErrInvalidInitData
	}

	authUnix, err := strconv.ParseInt(values["auth_date"], 10, 64)
	if err != nil {
		return InitData{}, ErrInvalidInitData
	}
	authDate := time.Unix(authUnix, 0)
	now := v.clock()
	if authDate.Before(now.Add(-v.maxAge)) || authDate.After(now.Add(v.futureGap)) {
		return InitData{}, ErrInvalidInitData
	}

	var user struct {
		ID json.Number `json:"id"`
	}
	decoder := json.NewDecoder(strings.NewReader(values["user"]))
	decoder.UseNumber()
	if err := decoder.Decode(&user); err != nil || user.ID == "" {
		return InitData{}, ErrInvalidInitData
	}
	userID, err := strconv.ParseUint(user.ID.String(), 10, 64)
	if err != nil || userID == 0 {
		return InitData{}, ErrInvalidInitData
	}

	return InitData{MaxUserID: strconv.FormatUint(userID, 10), AuthDate: authDate}, nil
}

func parseInitData(raw string) (map[string]string, string, string, error) {
	if raw == "" {
		return nil, "", "", ErrInvalidInitData
	}
	values := make(map[string]string)
	for _, pair := range strings.Split(raw, "&") {
		key, encodedValue, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return nil, "", "", ErrInvalidInitData
		}
		if _, exists := values[key]; exists {
			return nil, "", "", ErrInvalidInitData
		}
		value, err := url.QueryUnescape(encodedValue)
		if err != nil {
			return nil, "", "", ErrInvalidInitData
		}
		values[key] = value
	}
	hash, ok := values["hash"]
	if !ok {
		return nil, "", "", ErrInvalidInitData
	}
	delete(values, "hash")
	if values["auth_date"] == "" || values["user"] == "" {
		return nil, "", "", ErrInvalidInitData
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, values[key]))
	}
	return values, hash, strings.Join(parts, "\n"), nil
}
