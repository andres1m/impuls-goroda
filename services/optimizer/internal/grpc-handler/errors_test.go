package grpchandler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/usecase"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestToStatusCodes(t *testing.T) {
	wrap := func(err error) error { return fmt.Errorf("planning moscow: %w", err) }
	tests := []struct {
		name    string
		err     error
		code    codes.Code
		message string
	}{
		{"not implemented", usecase.ErrNotImplemented, codes.Unimplemented, usecase.ErrNotImplemented.Error()},
		{"wrapped not implemented", wrap(usecase.ErrNotImplemented), codes.Unimplemented, usecase.ErrNotImplemented.Error()},
		{"catalog not ready", wrap(usecase.ErrCatalogNotReady), codes.FailedPrecondition, usecase.ErrCatalogNotReady.Error()},
		{"stale catalog", wrap(usecase.ErrStaleCatalog), codes.FailedPrecondition, usecase.ErrStaleCatalog.Error()},
		{"unavailable", wrap(usecase.ErrUnavailable), codes.Unavailable, usecase.ErrUnavailable.Error()},
		{"overloaded", wrap(usecase.ErrOverloaded), codes.ResourceExhausted, usecase.ErrOverloaded.Error()},
		{"canceled", wrap(context.Canceled), codes.Canceled, context.Canceled.Error()},
		{"deadline", wrap(context.DeadlineExceeded), codes.DeadlineExceeded, context.DeadlineExceeded.Error()},
		{"unknown", errors.New("pgx: connection reset at 10.0.0.5"), codes.Internal, "internal error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := status.Convert(toStatus(zap.NewNop(), "/optimizer.v1.OptimizerService/Optimize", tt.err))
			if st.Code() != tt.code || st.Message() != tt.message {
				t.Fatalf("status = %s %q, want %s %q", st.Code(), st.Message(), tt.code, tt.message)
			}
		})
	}
}

func TestToStatusLogsInternalCause(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	toStatus(zap.New(core), "/optimizer.v1.OptimizerService/Recompute", errors.New("boom"))
	entries := logs.All()
	if len(entries) != 1 || entries[0].ContextMap()["error"] != "boom" || entries[0].ContextMap()["method"] != "/optimizer.v1.OptimizerService/Recompute" {
		t.Fatalf("logged %+v", entries)
	}
	core, logs = observer.New(zap.DebugLevel)
	toStatus(zap.New(core), "m", usecase.ErrNotImplemented)
	if logs.Len() != 0 {
		t.Fatal("expected errors must not be logged as failures")
	}
}

func TestInvalidArgumentDetails(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		field  string
		reason string
	}{
		{"field error", &fieldError{Field: "constraints.budget.mode", Reason: reasonUnsupported}, "constraints.budget.mode", reasonUnsupported},
		{"validation error", invalidRequest("base_plan", errors.New("plan interval is invalid")), "base_plan", "plan interval is invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := status.Convert(toStatus(zap.NewNop(), "m", tt.err))
			if st.Code() != codes.InvalidArgument || st.Message() != "invalid request" {
				t.Fatalf("status = %s %q", st.Code(), st.Message())
			}
			details := st.Details()
			if len(details) != 1 {
				t.Fatalf("details = %v", details)
			}
			br, ok := details[0].(*errdetails.BadRequest)
			if !ok || len(br.GetFieldViolations()) != 1 {
				t.Fatalf("details = %v", details)
			}
			v := br.GetFieldViolations()[0]
			if v.GetField() != tt.field || v.GetDescription() != tt.reason {
				t.Fatalf("violation = %q %q", v.GetField(), v.GetDescription())
			}
		})
	}
}

func TestInternalStatusHidesCause(t *testing.T) {
	st := status.Convert(toStatus(zap.NewNop(), "m", errors.New("secret 55.75,37.61")))
	if strings.Contains(st.Message(), "55.75") || len(st.Details()) != 0 {
		t.Fatalf("internal status leaks the cause: %v", st.Proto())
	}
}
