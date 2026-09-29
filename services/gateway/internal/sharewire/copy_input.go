package sharewire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"unicode/utf8"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

type CopyInput struct {
	Origin           d.Coordinate  `json:"origin"`
	Destination      *d.Coordinate `json:"destination,omitempty"`
	AcceptedUnknowns []string      `json:"accepted_unknowns"`
}

func DecodeCopyInput(raw []byte) (CopyInput, error) {
	if !utf8.Valid(raw) {
		return CopyInput{}, errInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return CopyInput{}, errInvalidInput
	}
	var input CopyInput
	seen := make(map[string]bool, 3)
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return CopyInput{}, errInvalidInput
		}
		seen[name] = true
		switch name {
		case "origin":
			input.Origin, err = copyCoordinate(decoder)
		case "destination":
			var point d.Coordinate
			point, err = copyCoordinate(decoder)
			input.Destination = &point
		case "accepted_unknowns":
			input.AcceptedUnknowns, err = copyUnknowns(decoder)
		default:
			err = errInvalidInput
		}
		if err != nil {
			return CopyInput{}, errInvalidInput
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') || !seen["origin"] || !seen["accepted_unknowns"] {
		return CopyInput{}, errInvalidInput
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return CopyInput{}, errInvalidInput
	}
	sort.Strings(input.AcceptedUnknowns)
	return input, nil
}

func copyCoordinate(decoder *json.Decoder) (d.Coordinate, error) {
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return d.Coordinate{}, errInvalidInput
	}
	var point d.Coordinate
	seen := make(map[string]bool, 2)
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return d.Coordinate{}, errInvalidInput
		}
		seen[name] = true
		token, err := decoder.Token()
		value, ok := token.(float64)
		if err != nil || !ok {
			return d.Coordinate{}, errInvalidInput
		}
		switch name {
		case "longitude":
			point.Longitude = value
		case "latitude":
			point.Latitude = value
		default:
			return d.Coordinate{}, errInvalidInput
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') || !seen["longitude"] || !seen["latitude"] || point.Validate() != nil {
		return d.Coordinate{}, errInvalidInput
	}
	return point, nil
}

func copyUnknowns(decoder *json.Decoder) ([]string, error) {
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('[') {
		return nil, errInvalidInput
	}
	values := make([]string, 0)
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		value, ok := token.(string)
		length := utf8.RuneCountInString(value)
		if err != nil || !ok || length < 1 || length > 128 || seen[value] {
			return nil, errInvalidInput
		}
		seen[value] = true
		values = append(values, value)
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim(']') {
		return nil, errInvalidInput
	}
	return values, nil
}
