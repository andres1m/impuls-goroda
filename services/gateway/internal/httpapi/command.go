package httpapi

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
)

func ParseIdempotencyKey(request *http.Request) ([16]byte, error) {
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return [16]byte{}, malformedCommandHeader()
	}
	value := values[0]
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return [16]byte{}, malformedCommandHeader()
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	if err != nil || len(decoded) != 16 {
		return [16]byte{}, malformedCommandHeader()
	}
	var key [16]byte
	copy(key[:], decoded)
	if key == ([16]byte{}) {
		return [16]byte{}, malformedCommandHeader()
	}
	return key, nil
}

func ParseIfMatch(request *http.Request) (domain.RouteRevisionNumber, error) {
	values := request.Header.Values("If-Match")
	if len(values) != 1 {
		return 0, malformedCommandHeader()
	}
	value := values[0]
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
		return 0, malformedCommandHeader()
	}
	digits := value[1 : len(value)-1]
	if digits[0] < '1' || digits[0] > '9' {
		return 0, malformedCommandHeader()
	}
	for _, digit := range digits[1:] {
		if digit < '0' || digit > '9' {
			return 0, malformedCommandHeader()
		}
	}
	parsed, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, malformedCommandHeader()
	}
	return domain.RouteRevisionNumber(parsed), nil
}

func MapCommandError(err error) error {
	if err == nil {
		return nil
	}
	var conflict *command.RevisionConflict
	if errors.As(err, &conflict) {
		return &Error{
			Status: http.StatusConflict, Code: "REVISION_CONFLICT", Message: "Route revision is stale",
			CurrentRevision: conflict.Current,
		}
	}
	if errors.Is(err, command.ErrIdempotencyKeyReused) {
		return &Error{Status: http.StatusConflict, Code: "IDEMPOTENCY_KEY_REUSED", Message: "Idempotency key was already used for another command"}
	}
	if errors.Is(err, postgres.ErrNotFound) {
		return &Error{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Resource not found"}
	}
	if errors.Is(err, postgres.ErrInvalidVisitAction) {
		return &Error{Status: http.StatusUnprocessableEntity, Code: "VALIDATION_FAILED", Message: "Visit action is invalid"}
	}
	return &Error{Status: http.StatusServiceUnavailable, Code: "DATABASE_UNAVAILABLE", Message: "Service is temporarily unavailable", Retryable: true, Cause: err}
}

func malformedCommandHeader() *Error {
	return &Error{Status: http.StatusBadRequest, Code: "MALFORMED_REQUEST", Message: "Request cannot be parsed"}
}
