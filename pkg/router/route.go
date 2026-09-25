package router

import (
	"context"

	"github.com/labstack/echo/v5"
)

type route struct {
	method      string
	path        string
	handler     func() echo.HandlerFunc
	middlewares []echo.MiddlewareFunc
}

func (r *route) Register(_ context.Context, g *echo.Group) {
	g.Add(r.method, r.path, r.handler(), r.middlewares...)
}

var _ Route = (*route)(nil)

func NewRoute(method, path string, handler func() echo.HandlerFunc, middlewares ...echo.MiddlewareFunc) Route {
	return &route{
		method:      method,
		path:        path,
		handler:     handler,
		middlewares: middlewares,
	}
}
