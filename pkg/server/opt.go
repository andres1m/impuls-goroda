package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/andres1m/impuls-goroda/pkg/router"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

const apiPrefix = "/api/v1"

// WithRouter mounts routers under /api/v1.
func WithRouter(ctx context.Context, routes ...router.Router) Option {
	return WithRouterGroup(ctx, "", routes...)
}

// WithRouterGroup mounts routers under /api/v1{prefix}, e.g. WithRouterGroup(ctx, "/routes", routeRouter).
func WithRouterGroup(ctx context.Context, prefix string, routes ...router.Router) Option {
	return func(s *Server) {
		g := s.api.Group(apiPrefix + prefix)

		for _, apiRouter := range routes {
			for _, route := range apiRouter.Routes() {
				route.Register(ctx, g)
			}
		}
	}
}

// WithHealth serves liveness at /healthz, outside the versioned API.
func WithHealth() Option {
	return func(s *Server) {
		s.api.GET("/healthz", func(c *echo.Context) error {
			return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
		})
	}
}

// WithMetrics serves Prometheus metrics at /metrics; mount it only on a server that is not public.
func WithMetrics() Option {
	return func(s *Server) {
		s.api.GET("/metrics", echo.WrapHandler(promhttp.Handler()))
	}
}

func WithDependsOn(names ...string) Option {
	return func(s *Server) {
		s.dependsOn = names
	}
}

func WithLogger(log *zap.Logger) Option {
	return func(s *Server) {
		s.log = log
		s.api.Use(middleware.RequestLoggerWithConfig(
			middleware.RequestLoggerConfig{
				HandleError:  true,
				LogURI:       true,
				LogStatus:    true,
				LogLatency:   true,
				LogRemoteIP:  true,
				LogMethod:    true,
				LogRequestID: true,

				LogValuesFunc: func(_ *echo.Context, v middleware.RequestLoggerValues) error {
					fields := []zap.Field{
						zap.String("method", v.Method),
						zap.String("uri", v.URI),
						zap.Int("status", v.Status),
						zap.Duration("latency", v.Latency),
						zap.String("remote_ip", v.RemoteIP),
						zap.String("request_id", v.RequestID),
					}

					if v.Error == nil {
						log.Info("request", fields...)

						return nil
					}

					var httpErr *echo.HTTPError
					if errors.As(v.Error, &httpErr) {
						fields = append(fields, zap.String("error_message", httpErr.Message))
					} else {
						fields = append(fields, zap.Error(v.Error))
					}
					log.Error("request", fields...)

					return nil
				},
			},
		))
	}
}

func WithMiddleware(middlewares ...echo.MiddlewareFunc) Option {
	return func(s *Server) {
		s.api.Use(middlewares...)
	}
}
