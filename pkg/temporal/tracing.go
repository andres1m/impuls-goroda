package temporal

import (
	"fmt"
	"slices"

	"go.temporal.io/sdk/client"
	temporalotel "go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
)

// withTracing carries the caller's trace into workflow headers, so a workflow and its
// activities join the trace of whatever started them. Workers built on the client inherit it.
func withTracing(opts client.Options) (client.Options, error) {
	tracing, err := temporalotel.NewTracingInterceptor(temporalotel.TracerOptions{})
	if err != nil {
		return opts, fmt.Errorf("temporal tracing: %w", err)
	}
	opts.Interceptors = append(slices.Clip(opts.Interceptors), interceptor.ClientInterceptor(tracing))
	return opts, nil
}
