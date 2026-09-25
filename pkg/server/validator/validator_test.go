package validator

import "testing"

func TestValidateRequiredField(t *testing.T) {
	type request struct {
		City string `validate:"required,oneof=moscow perm"`
	}
	v := NewValidator()

	if err := v.Validate(request{City: "perm"}); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if err := v.Validate(request{}); err == nil {
		t.Fatal("missing required field accepted")
	}
	if err := v.Validate(request{City: "kazan"}); err == nil {
		t.Fatal("value outside oneof accepted")
	}
}
