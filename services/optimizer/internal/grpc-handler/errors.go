package grpchandler

import (
	"context"
	"errors"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/usecase"
	"go.uber.org/zap"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// invalidRequest reports a domain validation failure against the top-level message it came from.
func invalidRequest(field string, err error) error {
	return &fieldError{Field: field, Reason: err.Error()}
}

var expectedErrors = []struct {
	err  error
	code codes.Code
}{
	{usecase.ErrNotImplemented, codes.Unimplemented},
	{usecase.ErrCatalogNotReady, codes.FailedPrecondition},
	{usecase.ErrStaleCatalog, codes.FailedPrecondition},
	{usecase.ErrUnavailable, codes.Unavailable},
	{usecase.ErrOverloaded, codes.ResourceExhausted},
	{context.Canceled, codes.Canceled},
	{context.DeadlineExceeded, codes.DeadlineExceeded},
}

// toStatus turns an error into the gRPC status the caller sees. Unexpected errors are
// logged here and reach the caller only as a generic internal error.
func toStatus(log *zap.Logger, method string, err error) error {
	var fe *fieldError
	if errors.As(err, &fe) {
		st := status.New(codes.InvalidArgument, "invalid request")
		detailed, detailErr := st.WithDetails(&errdetails.BadRequest{
			FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: fe.Field, Description: fe.Reason}},
		})
		if detailErr != nil {
			return st.Err()
		}
		return detailed.Err()
	}
	for _, expected := range expectedErrors {
		if errors.Is(err, expected.err) {
			return status.Error(expected.code, expected.err.Error())
		}
	}
	log.Error("request failed", zap.String("method", method), zap.Error(err))
	return status.Error(codes.Internal, "internal error")
}
