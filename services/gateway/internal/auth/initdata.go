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
	if decodeErr := decoder.Decode(&user); decodeErr != nil || user.ID == "" {
		return InitData{}, ErrInvalidInitData
	}
	userID, err := strconv.ParseUint(user.ID.String(), 10, 64)
	if err != nil || userID == 0 {
		return InitData{}, ErrInvalidInitData
	}

	return InitData{MaxUserID: strconv.FormatUint(userID, 10), AuthDate: authDate}, nil
}

func parseInitData(raw string) (values map[string]string, hash, dataCheck string, err error) {
	if raw == "" {
		return nil, "", "", ErrInvalidInitData
	}
	parsed := make(map[string]string)
	for pair := range strings.SplitSeq(raw, "&") {
		key, encodedValue, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return nil, "", "", ErrInvalidInitData
		}
		if _, exists := parsed[key]; exists {
			return nil, "", "", ErrInvalidInitData
		}
		value, unescapeErr := url.QueryUnescape(encodedValue)
		if unescapeErr != nil {
			return nil, "", "", ErrInvalidInitData
		}
		parsed[key] = value
	}
	parsedHash, ok := parsed["hash"]
	if !ok {
		return nil, "", "", ErrInvalidInitData
	}
	delete(parsed, "hash")
	if parsed["auth_date"] == "" || parsed["user"] == "" {
		return nil, "", "", ErrInvalidInitData
	}

	keys := make([]string, 0, len(parsed))
	for key := range parsed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, parsed[key]))
	}
	return parsed, parsedHash, strings.Join(parts, "\n"), nil
}
