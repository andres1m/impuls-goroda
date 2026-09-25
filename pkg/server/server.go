package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/server/validator"
	"github.com/andres1m/impuls-goroda/pkg/svc"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"go.uber.org/zap"
)

type Server struct {
	log       *zap.Logger
	name      string
	cfg       config.HTTPServer
	dependsOn []string
	api       *echo.Echo

	lis      net.Listener
	server   *http.Server
	stopOnce sync.Once
	stopErr  error
}

type Option func(*Server)

func (s *Server) Name() string {
	return s.name
}

func (s *Server) DependsOn() []string {
	return s.dependsOn
}

// Init binds the port so that a busy port fails startup instead of the running service.
func (s *Server) Init(ctx context.Context) error {
	if s.server != nil {
		return fmt.Errorf("%s server already initialized", s.name)
	}
	if s.cfg.Port < 0 || s.cfg.Port > 65535 {
		return fmt.Errorf("invalid %s server port %d", s.name, s.cfg.Port)
	}

	lis, err := (&net.ListenConfig{}).Listen(ctx, "tcp", fmt.Sprintf(":%d", s.cfg.Port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", s.cfg.Port, err)
	}

	s.lis = lis
	s.server = &http.Server{
		Handler:           s.api,
		ReadHeaderTimeout: s.cfg.ReadTimeout,
		ReadTimeout:       s.cfg.ReadTimeout,
		WriteTimeout:      s.cfg.WriteTimeout,
		IdleTimeout:       s.cfg.IdleTimeout,
	}

	s.log.Info("http server listening", zap.String("server", s.name), zap.String("addr", lis.Addr().String()))

	return nil
}

func (s *Server) HealthCheck(context.Context) error {
	if s.server == nil || s.lis == nil {
		return fmt.Errorf("%s server is not initialized", s.name)
	}

	return nil
}

func (s *Server) Run(context.Context) error {
	if s.server == nil {
		return fmt.Errorf("%s server is not initialized", s.name)
	}
	if err := s.server.Serve(s.lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("%s server failed: %w", s.name, err)
	}

	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	s.stopOnce.Do(func() {
		s.stopErr = s.server.Shutdown(ctx)
		// Shutdown only closes listeners that Serve has adopted; rollback before Run must release the port too.
		if err := s.lis.Close(); err != nil && !errors.Is(err, net.ErrClosed) && s.stopErr == nil {
			s.stopErr = err
		}
	})
	if s.stopErr != nil {
		return fmt.Errorf("failed to shutdown %s server gracefully: %w", s.name, s.stopErr)
	}

	return nil
}

// Addr is the bound address; it is known only after Init.
func (s *Server) Addr() net.Addr {
	if s.lis == nil {
		return nil
	}

	return s.lis.Addr()
}

func New(name string, cfg config.HTTPServer, opts ...Option) *Server {
	e := echo.New()
	e.Validator = validator.NewValidator()
	e.Use(middleware.Recover())

	s := &Server{
		log:       zap.NewNop(),
		name:      name,
		cfg:       cfg,
		dependsOn: []string{"logger"},
		api:       e,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

var _ svc.Service = (*Server)(nil)
