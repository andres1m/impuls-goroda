package grpchandler

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const maxRequestIDRunes = 128

// Recovery keeps a panicking call from taking the whole process down.
func Recovery(log *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if p := recover(); p != nil {
				log.Error("panic in handler", zap.String("method", info.FullMethod), zap.Any("panic", p), zap.Stack("stack"))
				resp, err = nil, status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}

// RequestLogging writes one line per call; request bodies are never logged because they
// carry user locations.
func RequestLogging(log *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		log.Info("grpc request",
			zap.String("method", info.FullMethod),
			zap.String("request_id", requestID(ctx)),
			zap.String("code", status.Code(err).String()),
			zap.Duration("duration", time.Since(start)),
		)
		return resp, err
	}
}

func requestID(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get("x-request-id")
	if len(values) == 0 {
		return ""
	}
	id := []rune(values[0])
	if len(id) > maxRequestIDRunes {
		id = id[:maxRequestIDRunes]
	}
	return string(id)
}
