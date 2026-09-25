package router

import (
	"context"

	"github.com/labstack/echo/v5"
)

type Router interface {
	Routes() []Route
}

type Route interface {
	Register(ctx context.Context, g *echo.Group)
}
