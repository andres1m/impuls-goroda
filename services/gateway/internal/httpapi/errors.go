package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/labstack/echo/v5"
)

type Error struct {
	Status          int
	Code            string
	Message         string
	Retryable       bool
	RetryAfter      int
	CurrentRevision domain.RouteRevisionNumber
	Cause           error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

type errorResponse struct {
	Code            string `json:"code"`
	Message         string `json:"message"`
	RequestID       string `json:"request_id"`
	Retryable       bool   `json:"retryable"`
	CurrentRevision string `json:"current_revision,omitempty"`
}

func ErrorHandler(c *echo.Context, err error) {
	if c.Response().(*echo.Response).Committed {
		return
	}
	apiError := classifyError(err)
	if apiError.RetryAfter > 0 {
		c.Response().Header().Set("Retry-After", strconv.Itoa(apiError.RetryAfter))
	}
	response := errorResponse{
		Code: apiError.Code, Message: apiError.Message,
		RequestID: RequestID(c), Retryable: apiError.Retryable,
	}
	if apiError.CurrentRevision > 0 {
		response.CurrentRevision = strconv.FormatInt(int64(apiError.CurrentRevision), 10)
		c.Response().Header().Set("ETag", `"`+response.CurrentRevision+`"`)
	}
	_ = c.JSON(apiError.Status, response)
}

func classifyError(err error) *Error {
	var apiError *Error
	if errors.As(err, &apiError) {
		return apiError
	}
	var echoError *echo.HTTPError
	if errors.As(err, &echoError) {
		switch echoError.Code {
		case http.StatusNotFound:
			return &Error{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Resource not found"}
		case http.StatusMethodNotAllowed:
			return &Error{Status: http.StatusMethodNotAllowed, Code: "METHOD_NOT_ALLOWED", Message: "Method not allowed"}
		case http.StatusRequestEntityTooLarge:
			return &Error{Status: http.StatusBadRequest, Code: "MALFORMED_REQUEST", Message: "Request cannot be parsed"}
		}
	}
	return &Error{
		Status:    http.StatusInternalServerError,
		Code:      "INTERNAL_ERROR",
		Message:   "Internal server error",
		Retryable: true,
		Cause:     err,
	}
}
