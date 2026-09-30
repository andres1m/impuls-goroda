package routewire

import "testing"

func TestDecodeLunchProposalInput(t *testing.T) {
	const anchor = "019c6949-009f-7a91-8854-7f2a93f1ca10"
	const lunch = "019c6949-00a0-7067-b329-787be513f307"
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"add external", `{"action":"add","placement":{"after_visit_id":"` + anchor + `","duration_seconds":2700},"venue":{"provider":"2gis","external_id":"123"}}`, true},
		{"add free time", `{"action":"add","placement":{"after_visit_id":"` + anchor + `","duration_seconds":3600}}`, true},
		{"update clear", `{"action":"update","lunch_id":"` + lunch + `","placement":{"after_visit_id":"` + anchor + `","duration_seconds":3000},"acknowledge_external_commitment":false}`, true},
		{"remove", `{"action":"remove","lunch_id":"` + lunch + `","acknowledge_external_commitment":true}`, true},
		{"duplicate action", `{"action":"add","action":"remove"}`, false},
		{"unknown field", `{"action":"remove","lunch_id":"` + lunch + `","acknowledge_external_commitment":false,"unexpected":true}`, false},
		{"bad duration", `{"action":"add","placement":{"after_visit_id":"` + anchor + `","duration_seconds":2699}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeLunchProposalInput([]byte(tc.body))
			if (err == nil) != tc.valid {
				t.Fatalf("decode error = %v, want valid %t", err, tc.valid)
			}
		})
	}
}
