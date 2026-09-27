package domain

import "testing"

func TestParseCity(t *testing.T) {
	for _, code := range []string{"moscow", "perm"} {
		city, err := ParseCity(code)
		if err != nil || string(city) != code {
			t.Fatalf("ParseCity(%q) = %q, %v", code, city, err)
		}
	}
	if _, err := ParseCity("spb"); err == nil {
		t.Fatal("ParseCity accepted an unknown city")
	}
}
