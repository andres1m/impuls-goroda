package main

import (
	"reflect"
	"testing"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

func TestParseBoundaryArgs(t *testing.T) {
	if got, err := parseBoundaryArgs(nil); err != nil || !reflect.DeepEqual(got, []domain.City{domain.Moscow, domain.Perm}) {
		t.Fatalf("no args: %v, %v", got, err)
	}
	if got, err := parseBoundaryArgs([]string{"perm"}); err != nil || !reflect.DeepEqual(got, []domain.City{domain.Perm}) {
		t.Fatalf("perm: %v, %v", got, err)
	}
	for _, args := range [][]string{{"kazan"}, {"perm", "moscow"}} {
		if _, err := parseBoundaryArgs(args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
