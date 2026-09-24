package svc

import "context"

// Service is initialized and health-checked before its dependents. Stop must
// tolerate partial initialization and repeated calls, and honor its context.
// Run may block until cancellation or return after starting a background worker.
// Methods other than Run are called serially by the lifecycle runner.
type Service interface {
	Name() string
	DependsOn() []string
	Init(context.Context) error
	HealthCheck(context.Context) error
	Run(context.Context) error
	Stop(context.Context) error
}
