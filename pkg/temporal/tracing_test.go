package temporal

import (
	"testing"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
)

func TestWithTracingAddsInterceptorToACopy(t *testing.T) {
	own := &interceptor.ClientInterceptorBase{}
	callers := make([]interceptor.ClientInterceptor, 1, 2)
	callers[0] = own
	opts, err := withTracing(client.Options{Interceptors: callers})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.Interceptors) != 2 || opts.Interceptors[0] != own {
		t.Fatalf("interceptors = %v", opts.Interceptors)
	}
	if callers[:2][1] != nil {
		t.Fatal("caller's backing array was written")
	}
}
