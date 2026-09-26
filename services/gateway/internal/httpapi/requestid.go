package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/labstack/echo/v5"
)

const requestIDKey = "request_id"

func RequestIDMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		requestID := c.Request().Header.Get(echo.HeaderXRequestID)
		if !validRequestID(requestID) {
			requestID = generateRequestID()
		}
		c.Set(requestIDKey, requestID)
		c.Response().Header().Set(echo.HeaderXRequestID, requestID)
		return next(c)
	}
}

func RequestID(c *echo.Context) string {
	requestID, _ := c.Get(requestIDKey).(string)
	if requestID == "" {
		requestID = generateRequestID()
		c.Set(requestIDKey, requestID)
		c.Response().Header().Set(echo.HeaderXRequestID, requestID)
	}
	return requestID
}

func validRequestID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	return strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') && !strings.ContainsRune("._:-", r)
	}) == -1
}

func generateRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "request-id-unavailable"
	}
	return hex.EncodeToString(raw[:])
}
