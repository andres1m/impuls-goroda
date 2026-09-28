package usecase

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func FuzzOptimize(f *testing.F) {
	for seed := range uint64(200) {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, seed uint64) {
		c := generate(seed)
		p, logs := plannerWithLog(t, c, 4)
		res, err := p.Optimize(context.Background(), c.req)
		if err != nil {
			t.Fatalf("optimize: %v", err)
		}
		if err := res.Validate(); err != nil {
			t.Fatalf("result: %v", err)
		}
		if n := rejections(logs); n > 0 {
			t.Fatalf("validator rejected %d routes: %v", n, logs.All()[0].ContextMap())
		}
		if v := checkOptimize(c.req, c.pool, res); len(v) > 0 {
			t.Fatal(describe(v))
		}
		for _, parallelism := range []int{4, 1} {
			again, _ := plannerWithLog(t, c, parallelism)
			other, err := again.Optimize(context.Background(), c.req)
			if err != nil {
				t.Fatalf("optimize again: %v", err)
			}
			if other.Status != res.Status || len(other.Routes) != len(res.Routes) {
				t.Fatalf("parallelism %d: %s with %d routes, first run %s with %d", parallelism, other.Status, len(other.Routes), res.Status, len(res.Routes))
			}
			for i := range res.Routes {
				if !slices.Equal(signature(res.Routes[i]), signature(other.Routes[i])) || !reflect.DeepEqual(res.Routes[i].Cost, other.Routes[i].Cost) {
					t.Fatalf("parallelism %d: route %d differs", parallelism, i)
				}
			}
		}
	})
}

func TestGeneratedOutcomesAreVaried(t *testing.T) {
	counts := map[domain.ResultStatus]int{}
	for seed := range uint64(200) {
		c := generate(seed)
		p, _ := plannerWithLog(t, c, 4)
		res, err := p.Optimize(context.Background(), c.req)
		if err != nil {
			t.Fatal(err)
		}
		counts[res.Status]++
	}
	t.Logf("outcomes over 200 seeds: %v", counts)
	if counts[domain.ResultReady]+counts[domain.ResultPartial] < 100 || counts[domain.ResultNoFeasibleRoute]+counts[domain.ResultConflict] == 0 {
		t.Fatalf("outcomes %v", counts)
	}
}
