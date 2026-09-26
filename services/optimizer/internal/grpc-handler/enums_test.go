package grpchandler

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// checkEnum proves that every wire value except UNSPECIFIED maps to the domain value of the
// same name and back, so a new or mistyped value cannot slip through unnoticed.
func checkEnum[P interface {
	~int32
	protoreflect.Enum
}, D ~string](t *testing.T, m enumMap[P, D]) {
	t.Helper()
	var zero P
	values := zero.Descriptor().Values()
	prefix := strings.TrimSuffix(string(values.ByNumber(0).Name()), "UNSPECIFIED")
	if len(m.toDomain) != values.Len()-1 || len(m.toProto) != len(m.toDomain) {
		t.Errorf("%s: table has %d/%d entries, want %d", zero.Descriptor().Name(), len(m.toDomain), len(m.toProto), values.Len()-1)
	}
	if _, ok := m.toDomain[zero]; ok {
		t.Errorf("%s: UNSPECIFIED must not map to a domain value", zero.Descriptor().Name())
	}
	for i := 0; i < values.Len(); i++ {
		v := values.Get(i)
		if v.Number() == 0 {
			continue
		}
		p := P(v.Number())
		d, ok := m.toDomain[p]
		want := strings.ToLower(strings.TrimPrefix(string(v.Name()), prefix))
		if !ok || strings.ToLower(string(d)) != want {
			t.Errorf("%s maps to %q, want %q", v.Name(), d, want)
		}
		if m.toProto[d] != p {
			t.Errorf("%q maps back to %v, want %s", d, m.toProto[d], v.Name())
		}
	}
}

func TestEnumTablesAreComplete(t *testing.T) {
	checkEnum(t, priceStatuses)
	checkEnum(t, dataModes)
	checkEnum(t, categories)
	checkEnum(t, archetypes)
	checkEnum(t, participationStatuses)
	checkEnum(t, verificationStatuses)
	checkEnum(t, budgetModes)
	checkEnum(t, resultStatuses)
	checkEnum(t, availabilities)
	checkEnum(t, budgetConclusions)
	checkEnum(t, participationEvidences)
	checkEnum(t, constraintStrengths)
	checkEnum(t, constraintOutcomes)
	checkEnum(t, stepKinds)
	checkEnum(t, legEndpoints)
	checkEnum(t, scopes)
	checkEnum(t, executionStatuses)
	checkEnum(t, delayModes)
	checkEnum(t, positionSources)
	checkEnum(t, removalModes)
	checkEnum(t, pinKinds)
	checkEnum(t, recomputeStatuses)
	checkEnum(t, changeKinds)
}
