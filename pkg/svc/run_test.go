package svc

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"go.uber.org/zap"
)

type component struct {
	name                       string
	deps                       []string
	events                     *[]string
	initErr, healthErr, runErr error
}

func (s *component) Name() string        { return s.name }
func (s *component) DependsOn() []string { return s.deps }
func (s *component) Init(context.Context) error {
	*s.events = append(*s.events, "init:"+s.name)
	return s.initErr
}
func (s *component) HealthCheck(context.Context) error { return s.healthErr }
func (s *component) Run(context.Context) error         { return s.runErr }
func (s *component) Stop(context.Context) error {
	*s.events = append(*s.events, "stop:"+s.name)
	return nil
}

func TestStartupFailureRollsBack(t *testing.T) {
	for _, stage := range []string{"init", "health"} {
		t.Run(stage, func(t *testing.T) {
			events := []string{}
			failure := errors.New("failed")
			a := &component{name: "a", events: &events}
			b := &component{name: "b", deps: []string{"a"}, events: &events}
			if stage == "init" {
				b.initErr = failure
			} else {
				b.healthErr = failure
			}
			err := Run(context.Background(), zap.NewNop(), []Service{b, a})
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v", err)
			}
			want := []string{"init:a", "init:b", "stop:b", "stop:a"}
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("events = %v; want %v", events, want)
			}
		})
	}
}
func TestInvalidDependencies(t *testing.T) {
	events := []string{}
	for _, ss := range [][]Service{
		{&component{name: "a", deps: []string{"missing"}, events: &events}},
		{&component{name: "a", deps: []string{"b"}, events: &events}, &component{name: "b", deps: []string{"a"}, events: &events}},
		{&component{name: "a", events: &events}, &component{name: "a", events: &events}},
	} {
		if err := Run(context.Background(), zap.NewNop(), ss); err == nil {
			t.Fatal("expected dependency error")
		}
	}
	if len(events) != 0 {
		t.Fatalf("invalid graph initialized: %v", events)
	}
}
func TestRunFailureStopsDependencies(t *testing.T) {
	events := []string{}
	failure := errors.New("run failed")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := Run(ctx, zap.NewNop(), []Service{&component{name: "a", events: &events, runErr: failure}})
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
	if !reflect.DeepEqual(events, []string{"init:a", "stop:a"}) {
		t.Fatal(events)
	}
}

func TestResourceOnlyRunWaitsForCancellation(t *testing.T) {
	for i := 0; i < 30; i++ {
		events := []string{}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- Run(ctx, zap.NewNop(), []Service{&component{name: "resource", events: &events}}) }()
		select {
		case err := <-done:
			cancel()
			t.Fatalf("resource-only service exited without cancellation: %v", err)
		case <-time.After(5 * time.Millisecond):
			cancel()
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("did not stop")
		}
	}
}
