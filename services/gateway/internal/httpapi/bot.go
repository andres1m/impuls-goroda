package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/maxbot"
	"github.com/labstack/echo/v5"
)

type BotHandler interface {
	Handle(context.Context, maxbot.Update) error
}

type BotRouter struct {
	checker WebhookChecker
	bot     BotHandler
}

func NewBotRouter(checker WebhookChecker, bot BotHandler) *BotRouter {
	return &BotRouter{checker: checker, bot: bot}
}

func (r *BotRouter) Routes() []router.Route {
	return []router.Route{router.NewRoute(http.MethodPost, "/max/webhook", r.webhook, VerifyWebhook(r.checker))}
}

func (r *BotRouter) webhook() echo.HandlerFunc {
	return func(c *echo.Context) error {
		body := http.MaxBytesReader(c.Response(), c.Request().Body, 64*1024)
		decoder := json.NewDecoder(body)
		var update maxbot.Update
		if err := decoder.Decode(&update); err != nil {
			return &Error{Status: http.StatusBadRequest, Code: "MALFORMED_REQUEST", Message: "Request cannot be parsed"}
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return &Error{Status: http.StatusBadRequest, Code: "MALFORMED_REQUEST", Message: "Request cannot be parsed"}
		}
		if err := r.bot.Handle(c.Request().Context(), update); err != nil {
			return &Error{Status: http.StatusServiceUnavailable, Code: "BOT_UNAVAILABLE", Message: "Bot is temporarily unavailable", Retryable: true, Cause: err}
		}
		return c.NoContent(http.StatusOK)
	}
}
