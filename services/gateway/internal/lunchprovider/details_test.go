package lunchprovider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOrganizationVerifiesCafeRubric(t *testing.T) {
	for _, tc := range []struct {
		name, rubrics string
		wantOK        bool
	}{
		{"cafe", `[{"id":"161"}]`, true},
		{"missing", `[]`, false},
		{"other", `[{"id":"999"}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewClient("test-key")
			if err != nil {
				t.Fatal(err)
			}
			client.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if !strings.Contains(req.URL.Query().Get("fields"), "items.rubrics") {
					t.Fatal("rubrics were not requested")
				}
				body := `{"meta":{"code":200},"result":{"items":[{"id":"123","name":"Cafe","type":"branch","address_name":"Street","point":{"lat":55.7,"lon":37.6},"rubrics":` + tc.rubrics + `}]}}`
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			_, err = client.Organization(context.Background(), "123")
			if (err == nil) != tc.wantOK {
				t.Fatalf("Organization error = %v, want success %t", err, tc.wantOK)
			}
		})
	}
}
