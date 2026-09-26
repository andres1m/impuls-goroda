package rpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"github.com/andres1m/impuls-goroda/pkg/svc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type Server struct {
	log  *zap.Logger
	cfg  *config.GRPCServer
	name string

	lis    net.Listener
	server *grpc.Server

	onInit   []func(*Server)
	unary    []grpc.UnaryServerInterceptor
	stopOnce sync.Once
	stopped  chan struct{}
}

func (s *Server) GetServer() *grpc.Server {
	return s.server
}

func (s *Server) OnInit(fn func(*Server)) {
	s.onInit = append(s.onInit, fn)
}

func (s *Server) DependsOn() []string {
	return []string{"logger"}
}

func (s *Server) HealthCheck(ctx context.Context) error {
	if s.server == nil {
		return fmt.Errorf("grpc server is not initialized")
	}

	if s.lis == nil {
		return fmt.Errorf("grpc server listener is not bound")
	}

	return nil
}

func (s *Server) Init(ctx context.Context) error {
	if s.server != nil {
		return errors.New("grpc server already initialized")
	}
	if s.cfg.Port < 0 || s.cfg.Port > 65535 {
		return errors.New("invalid grpc server port")
	}
	opts := []grpc.ServerOption{}

	if s.cfg.MaxRecvMsgSize > 0 {
		opts = append(opts, grpc.MaxRecvMsgSize(s.cfg.MaxRecvMsgSize))
	}

	if s.cfg.UseTLS {
		cert, err := tls.LoadX509KeyPair(s.cfg.TLS.ServerCertPath, s.cfg.TLS.ServerKeyPath)
		if err != nil {
			return fmt.Errorf("failed to load x509 key pair: %w", err)
		}

		caCertPool := x509.NewCertPool()

		caBytes, err := os.ReadFile(s.cfg.TLS.CaCertPath)
		if err != nil {
			return fmt.Errorf("failed to read ca cert: %w", err)
		}

		if ok := caCertPool.AppendCertsFromPEM(caBytes); !ok {
			return fmt.Errorf("failed to append ca cert")
		}

		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    caCertPool,
			MinVersion:   tls.VersionTLS13,
		}

		opts = append(opts, grpc.Creds(credentials.NewTLS(tlsConfig)))
	}

	if len(s.unary) > 0 {
		opts = append(opts, grpc.ChainUnaryInterceptor(s.unary...))
	}

	server := grpc.NewServer(opts...)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.cfg.Port))
	if err != nil {
		return fmt.Errorf("failed to listen on %d port: %w", s.cfg.Port, err)
	}

	s.lis = lis
	s.server = server
	s.stopped = make(chan struct{})

	for _, fn := range s.onInit {
		fn(s)
	}

	s.log.Info("grpc server started", zap.Int("port", s.cfg.Port))

	return nil
}

func (s *Server) Name() string {
	return fmt.Sprintf("grpc-server-%s", s.name)
}

func (s *Server) Run(ctx context.Context) error {
	if s.server == nil {
		return errors.New("grpc server is not initialized")
	}
	if err := s.server.Serve(s.lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("failed to serve: %w", err)
	}

	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	s.stopOnce.Do(func() {
		go func() {
			s.server.GracefulStop()
			// A listener belongs to gRPC only after Serve starts; rollback must close it too.
			if s.lis != nil {
				_ = s.lis.Close()
			}
			close(s.stopped)
		}()
	})
	select {
	case <-s.stopped:
		return nil
	case <-ctx.Done():
		s.server.Stop()
		return ctx.Err()
	}
}

type ServerOption func(*Server)

// WithUnaryInterceptors chains the interceptors in the given order; the first one runs outermost.
func WithUnaryInterceptors(interceptors ...grpc.UnaryServerInterceptor) ServerOption {
	return func(s *Server) {
		s.unary = append(s.unary, interceptors...)
	}
}

func NewServer(name string, log *zap.Logger, cfg *config.GRPCServer, opts ...ServerOption) *Server {
	if log == nil {
		log = zap.NewNop()
	}
	if cfg == nil {
		cfg = &config.GRPCServer{}
	}
	copied := *cfg
	s := &Server{
		name: name,
		log:  log,
		cfg:  &copied,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

var _ svc.Service = (*Server)(nil)
