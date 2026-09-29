package routewire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"
)

var ErrInvalidNotificationPreference = errors.New("invalid notification preference")

type NotificationPreferenceInput struct {
	Enabled         bool   `json:"enabled"`
	ExpectedVersion string `json:"expected_version"`
}

type NotificationPreference struct {
	Enabled       bool   `json:"enabled"`
	Version       string `json:"version"`
	PlatformState string `json:"platform_state"`
}

type NotificationPreferenceResponse struct {
	RouteID    string                 `json:"route_id"`
	Revision   string                 `json:"revision"`
	Preference NotificationPreference `json:"preference"`
}

func DecodeNotificationPreference(raw []byte) (NotificationPreferenceInput, error) {
	var input NotificationPreferenceInput
	if len(raw) == 0 || len(raw) > 1024 || !utf8.Valid(raw) {
		return input, ErrInvalidNotificationPreference
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := readJSON(decoder, 0)
	if err != nil {
		return input, ErrInvalidNotificationPreference
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return input, ErrInvalidNotificationPreference
	}
	fields, ok := value.(map[string]any)
	if !ok || len(fields) != 2 {
		return input, ErrInvalidNotificationPreference
	}
	enabled, validEnabled := fields["enabled"].(bool)
	version, validVersion := fields["expected_version"].(string)
	number, err := strconv.ParseInt(version, 10, 64)
	if !validEnabled || !validVersion || err != nil || number < 0 || strconv.FormatInt(number, 10) != version {
		return input, ErrInvalidNotificationPreference
	}
	return NotificationPreferenceInput{Enabled: enabled, ExpectedVersion: version}, nil
}
