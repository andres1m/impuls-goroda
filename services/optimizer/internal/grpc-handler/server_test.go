package grpchandler

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	pb "github.com/andres1m/impuls-goroda/proto/optimizer/v1"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/usecase"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type fakePlanner struct {
	optimize  func(context.Context, domain.OptimizeRequest) (domain.OptimizeResult, error)
	recompute func(context.Context, domain.RecomputeRequest) (domain.RecomputeResult, error)
}

func (f fakePlanner) Optimize(ctx context.Context, r domain.OptimizeRequest) (domain.OptimizeResult, error) {
	return f.optimize(ctx, r)
}

func (f fakePlanner) Recompute(ctx context.Context, r domain.RecomputeRequest) (domain.RecomputeResult, error) {
	return f.recompute(ctx, r)
}

type testServer struct {
	client pb.OptimizerServiceClient
	health healthpb.HealthClient
	logs   *observer.ObservedLogs
}

func startServer(t *testing.T, planner Planner) testServer {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	log := zap.New(core)
	lis := bufconn.Listen(1 << 20)
	s := grpc.NewServer(grpc.ChainUnaryInterceptor(RequestLogging(log), Recovery(log)))
	NewHandler(log, planner).Register(s)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return testServer{client: pb.NewOptimizerServiceClient(conn), health: healthpb.NewHealthClient(conn), logs: logs}
}

func callContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func requireCode(t *testing.T, err error, code codes.Code) *status.Status {
	t.Helper()
	st := status.Convert(err)
	if st.Code() != code {
		t.Fatalf("status = %s %q, want %s", st.Code(), st.Message(), code)
	}
	return st
}

func requireViolation(t *testing.T, err error, field string) {
	t.Helper()
	st := requireCode(t, err, codes.InvalidArgument)
	for _, d := range st.Details() {
		if br, ok := d.(*errdetails.BadRequest); ok && len(br.GetFieldViolations()) == 1 && br.GetFieldViolations()[0].GetField() == field {
			return
		}
	}
	t.Fatalf("details = %v, want a violation of %q", st.Details(), field)
}

func validOptimizeResult() domain.OptimizeResult {
	return domain.OptimizeResult{
		Status:          domain.ResultPartial,
		Routes:          []domain.Plan{domainPlan()},
		Data:            domain.DataFreshness{DataMode: domain.DataPrepared, CatalogRevision: 42},
		ComputationTime: 7 * time.Millisecond,
	}
}

func validRecomputeResult() domain.RecomputeResult {
	candidate := domainPlan()
	return domain.RecomputeResult{
		Status:    domain.RecomputeProposed,
		Candidate: &candidate,
		Data:      domain.DataFreshness{DataMode: domain.DataPrepared, CatalogRevision: 42},
	}
}

func returning(o domain.OptimizeResult, r domain.RecomputeResult) fakePlanner {
	return fakePlanner{
		optimize:  func(context.Context, domain.OptimizeRequest) (domain.OptimizeResult, error) { return o, nil },
		recompute: func(context.Context, domain.RecomputeRequest) (domain.RecomputeResult, error) { return r, nil },
	}
}

func TestServerRejectsMalformedRequests(t *testing.T) {
	srv := startServer(t, usecase.NewPlanner())
	ctx := callContext(t)

	in := pbOptimizeRequest()
	in.Constraints = nil
	_, err := srv.client.Optimize(ctx, in)
	requireViolation(t, err, "constraints")

	in = pbOptimizeRequest()
	in.EndAt = in.StartAt
	_, err = srv.client.Optimize(ctx, in)
	requireViolation(t, err, "request")

	rin := pbRecomputeRequest()
	rin.Trigger = nil
	_, err = srv.client.Recompute(ctx, rin)
	requireViolation(t, err, "trigger")

	rin = pbRecomputeRequest()
	rin.Trigger = &pb.RecomputeRequest_Pin{Pin: &pb.PinTrigger{VisitId: id(9), Kind: pb.PinKind_PIN_KIND_PREFERRED}}
	_, err = srv.client.Recompute(ctx, rin)
	requireViolation(t, err, "request")
}

func TestServerIsHonestlyUnimplemented(t *testing.T) {
	srv := startServer(t, usecase.NewPlanner())
	ctx := callContext(t)
	_, err := srv.client.Optimize(ctx, pbOptimizeRequest())
	requireCode(t, err, codes.Unimplemented)
	_, err = srv.client.Recompute(ctx, pbRecomputeRequest())
	requireCode(t, err, codes.Unimplemented)
}

func TestServerReturnsPlannerResults(t *testing.T) {
	var gotOptimize domain.OptimizeRequest
	var gotRecompute domain.RecomputeRequest
	planner := fakePlanner{
		optimize: func(_ context.Context, r domain.OptimizeRequest) (domain.OptimizeResult, error) {
			gotOptimize = r
			return validOptimizeResult(), nil
		},
		recompute: func(_ context.Context, r domain.RecomputeRequest) (domain.RecomputeResult, error) {
			gotRecompute = r
			return validRecomputeResult(), nil
		},
	}
	srv := startServer(t, planner)
	ctx := callContext(t)

	out, err := srv.client.Optimize(ctx, pbOptimizeRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(out, optimizeResponseToProto(validOptimizeResult())) {
		t.Fatalf("optimize response = %v", out)
	}
	if gotOptimize.City != "moscow" || len(gotOptimize.Constraints.Obligations) != 2 {
		t.Fatalf("planner received %+v", gotOptimize)
	}

	rout, err := srv.client.Recompute(ctx, pbRecomputeRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(rout, recomputeResponseToProto(validRecomputeResult())) {
		t.Fatalf("recompute response = %v", rout)
	}
	if _, ok := gotRecompute.Trigger.(domain.PinTrigger); !ok || len(gotRecompute.Base.Steps) != 2 {
		t.Fatalf("planner received %+v", gotRecompute)
	}
}

func TestServerRejectsInvalidPlannerResults(t *testing.T) {
	brokenRoute := validOptimizeResult()
	brokenRoute.Routes[0].Steps[0].Position = 9
	budgetHidden := validOptimizeResult()
	budgetHidden.Routes[0].Cost.BudgetConclusion = domain.BudgetSatisfied
	brokenCandidate := validRecomputeResult()
	brokenCandidate.Candidate.Legs = nil
	candidateBudgetHidden := validRecomputeResult()
	candidateBudgetHidden.Candidate.Cost.BudgetConclusion = domain.BudgetSatisfied

	tests := []struct {
		name           string
		planner        fakePlanner
		brokenOptimize bool
	}{
		{"invalid route", returning(brokenRoute, validRecomputeResult()), true},
		{"route hides unknown strict budget", returning(budgetHidden, validRecomputeResult()), true},
		{"invalid candidate", returning(validOptimizeResult(), brokenCandidate), false},
		{"candidate hides unknown strict budget", returning(validOptimizeResult(), candidateBudgetHidden), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := startServer(t, tt.planner)
			ctx := callContext(t)
			_, oerr := srv.client.Optimize(ctx, pbOptimizeRequest())
			_, rerr := srv.client.Recompute(ctx, pbRecomputeRequest())
			broken, healthy := oerr, rerr
			if !tt.brokenOptimize {
				broken, healthy = rerr, oerr
			}
			requireCode(t, broken, codes.Internal)
			if healthy != nil {
				t.Fatalf("the other method failed: %v", healthy)
			}
			if srv.logs.FilterMessage("request failed").Len() != 1 {
				t.Fatalf("invalid result was not logged: %+v", srv.logs.All())
			}
		})
	}
}

func TestServerSurvivesPlannerPanic(t *testing.T) {
	calls := 0
	planner := fakePlanner{
		optimize: func(context.Context, domain.OptimizeRequest) (domain.OptimizeResult, error) {
			calls++
			if calls == 1 {
				panic("solver bug")
			}
			return validOptimizeResult(), nil
		},
		recompute: func(context.Context, domain.RecomputeRequest) (domain.RecomputeResult, error) {
			panic("solver bug")
		},
	}
	srv := startServer(t, planner)
	ctx := callContext(t)
	_, err := srv.client.Optimize(ctx, pbOptimizeRequest())
	requireCode(t, err, codes.Internal)
	_, err = srv.client.Recompute(ctx, pbRecomputeRequest())
	requireCode(t, err, codes.Internal)
	if _, err := srv.client.Optimize(ctx, pbOptimizeRequest()); err != nil {
		t.Fatalf("server did not recover: %v", err)
	}
	panics := srv.logs.FilterMessage("panic in handler").All()
	if len(panics) != 2 || panics[0].ContextMap()["method"] != pb.OptimizerService_Optimize_FullMethodName {
		t.Fatalf("panic logs = %+v", panics)
	}
}

func TestServerMapsPlannerErrors(t *testing.T) {
	planner := fakePlanner{
		optimize: func(context.Context, domain.OptimizeRequest) (domain.OptimizeResult, error) {
			return domain.OptimizeResult{}, usecase.ErrStaleCatalog
		},
		recompute: func(context.Context, domain.RecomputeRequest) (domain.RecomputeResult, error) {
			return domain.RecomputeResult{}, usecase.ErrOverloaded
		},
	}
	srv := startServer(t, planner)
	ctx := callContext(t)
	_, err := srv.client.Optimize(ctx, pbOptimizeRequest())
	requireCode(t, err, codes.FailedPrecondition)
	_, err = srv.client.Recompute(ctx, pbRecomputeRequest())
	requireCode(t, err, codes.ResourceExhausted)
}

func TestServerHealth(t *testing.T) {
	srv := startServer(t, usecase.NewPlanner())
	ctx := callContext(t)
	for _, service := range []string{"", "optimizer.v1.OptimizerService"} {
		out, err := srv.health.Check(ctx, &healthpb.HealthCheckRequest{Service: service})
		if err != nil || out.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			t.Fatalf("health %q = %v, %v", service, out, err)
		}
	}
}

func TestServerLogsRequests(t *testing.T) {
	srv := startServer(t, usecase.NewPlanner())
	ctx := metadata.AppendToOutgoingContext(callContext(t), "x-request-id", "req-42")
	_, _ = srv.client.Optimize(ctx, pbOptimizeRequest())

	long := strings.Repeat("a", 200)
	ctx = metadata.AppendToOutgoingContext(callContext(t), "x-request-id", long)
	_, _ = srv.client.Recompute(ctx, pbRecomputeRequest())

	_, _ = srv.client.Optimize(callContext(t), pbOptimizeRequest())

	entries := srv.logs.FilterMessage("grpc request").All()
	if len(entries) != 3 {
		t.Fatalf("request logs = %+v", entries)
	}
	first := entries[0].ContextMap()
	if first["request_id"] != "req-42" || first["code"] != codes.Unimplemented.String() || first["method"] != pb.OptimizerService_Optimize_FullMethodName {
		t.Fatalf("first log = %v", first)
	}
	if _, ok := first["duration"]; !ok {
		t.Fatalf("first log has no duration: %v", first)
	}
	if got := entries[1].ContextMap()["request_id"]; got != strings.Repeat("a", maxRequestIDRunes) {
		t.Fatalf("long request id logged as %q", got)
	}
	if got := entries[2].ContextMap()["request_id"]; got != "" {
		t.Fatalf("missing request id logged as %q", got)
	}
	for _, e := range srv.logs.All() {
		for _, v := range e.ContextMap() {
			if s, ok := v.(string); ok && (strings.Contains(s, "55.69") || strings.Contains(s, "37.59")) {
				t.Fatalf("log leaks coordinates: %+v", e)
			}
		}
	}
}
