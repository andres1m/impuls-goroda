package routewire

import (
	"reflect"
	"testing"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

func TestRecipientCopyConstraintsContainOnlyRecipientChoices(t *testing.T) {
	accepted := []string{"price_unknown"}
	got := recipientCopyConstraints(accepted)
	want := d.RouteConstraints{
		MovementModes:    []d.MovementMode{"walk"},
		LoadProfile:      "standard",
		Budget:           d.Budget{Mode: d.BudgetNone},
		AcceptedUnknowns: []d.UnknownConditionCode{"price_unknown"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("copy constraints include unexpected preferences: got %+v, want %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	accepted[0] = "changed"
	if got.AcceptedUnknowns[0] != "price_unknown" {
		t.Fatal("recipient confirmations alias request input")
	}
}
