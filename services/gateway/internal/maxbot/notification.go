package maxbot

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

type NotificationSendError struct {
	Kind            string
	HTTPStatus      int
	Retryable       bool
	DeliveryUnknown bool
	RetryAfter      time.Duration
}

func (e *NotificationSendError) Error() string { return "MAX notification: " + e.Kind }

type NotificationReceipt struct{ MessageID string }

func (c *Client) SendNotification(ctx context.Context, userID int64, text string) (NotificationReceipt, error) {
	return c.sendNotification(ctx, userID, message{Text: text})
}

func (c *Client) SendRouteNotification(ctx context.Context, userID int64, routeID uuid.UUID, text string) (NotificationReceipt, error) {
	if _, err := c.OwnerRouteURL(routeID); err != nil {
		return NotificationReceipt{}, &NotificationSendError{Kind: "invalid_input"}
	}
	content := message{Text: text}
	content.addKeyboard([][]button{{{Type: "open_app", Text: "Открыть маршрут", WebApp: c.username, Payload: "route_" + routeID.String()}}})
	return c.sendNotification(ctx, userID, content)
}

func (c *Client) sendNotification(ctx context.Context, userID int64, content message) (NotificationReceipt, error) {
	if c == nil || c.http == nil || c.token == "" || userID <= 0 || !utf8.ValidString(content.Text) || strings.TrimSpace(content.Text) == "" || utf8.RuneCountInString(content.Text) > 4000 {
		return NotificationReceipt{}, &NotificationSendError{Kind: "invalid_input"}
	}
	body, err := json.Marshal(content)
	if err != nil {
		return NotificationReceipt{}, &NotificationSendError{Kind: "invalid_input"}
	}
	endpoint := c.baseURL + "/messages?user_id=" + url.QueryEscape(strconv.FormatInt(userID, 10))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return NotificationReceipt{}, &NotificationSendError{Kind: "invalid_input"}
	}
	request.Header.Set("Authorization", c.token)
	request.Header.Set("Content-Type", "application/json")
	release, err := c.sends.acquire(ctx, userID)
	if err != nil {
		return NotificationReceipt{}, &NotificationSendError{Kind: "send_capacity_unavailable", Retryable: true, RetryAfter: recipientSendInterval}
	}
	defer release()
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return NotificationReceipt{}, &NotificationSendError{Kind: "transport_unavailable", Retryable: true, DeliveryUnknown: true}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseDrainBytes))
		failure := &NotificationSendError{Kind: "rejected", HTTPStatus: response.StatusCode}
		switch {
		case response.StatusCode == http.StatusUnauthorized:
			failure.Kind = "credentials_rejected"
		case response.StatusCode == http.StatusTooManyRequests:
			failure.Kind, failure.Retryable = "rate_limited", true
			failure.RetryAfter = notificationRetryAfter(response.Header.Get("Retry-After"), time.Now())
		case response.StatusCode >= 500:
			failure.Kind, failure.Retryable, failure.DeliveryUnknown = "server_unavailable", true, true
		}
		return NotificationReceipt{}, failure
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseDrainBytes+1))
	if err != nil || len(raw) > maxResponseDrainBytes {
		return NotificationReceipt{}, &NotificationSendError{Kind: "invalid_response", HTTPStatus: response.StatusCode, Retryable: true, DeliveryUnknown: true}
	}
	var result struct {
		Message *struct {
			Body *struct {
				ID string `json:"mid"`
			} `json:"body"`
			Recipient struct {
				UserID *int64 `json:"user_id"`
			} `json:"recipient"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Message == nil || result.Message.Body == nil || !validNotificationMessageID(result.Message.Body.ID) || result.Message.Recipient.UserID != nil && *result.Message.Recipient.UserID != userID {
		return NotificationReceipt{}, &NotificationSendError{Kind: "invalid_response", HTTPStatus: response.StatusCode, Retryable: true, DeliveryUnknown: true}
	}
	return NotificationReceipt{MessageID: result.Message.Body.ID}, nil
}

func validNotificationMessageID(id string) bool {
	if id == "" || len(id) > 512 || !utf8.ValidString(id) {
		return false
	}
	for _, character := range id {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func notificationRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil {
		if seconds > uint64(math.MaxInt64/int64(time.Second)) {
			return time.Duration(math.MaxInt64)
		}
		return time.Duration(seconds) * time.Second
	}
	if until, err := http.ParseTime(value); err == nil && until.After(now) {
		return until.Sub(now)
	}
	return 0
}
