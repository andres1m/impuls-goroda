package command

import (
	"encoding/json"
	"testing"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

func TestFingerprintUsesNormalizedCommandIdentity(t *testing.T) {
	actor := domain.UserID{1}
	revision := domain.RouteRevisionNumber(7)
	input := FingerprintInput{
		ActorID: actor, Operation: SaveRoute,
		Path:             []PathComponent{{Name: "route_id", Value: "route-one"}},
		ExpectedRevision: &revision,
		Body:             map[string]any{"second": 2, "first": []string{"a", "b"}},
	}
	initial, err := Fingerprint(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Body = map[string]any{"first": []string{"a", "b"}, "second": 2}
	reordered, err := Fingerprint(input)
	if err != nil || initial != reordered {
		t.Fatal("equivalent normalized body changed fingerprint")
	}
	changed := []FingerprintInput{
		{ActorID: domain.UserID{2}, Operation: input.Operation, Path: input.Path, ExpectedRevision: input.ExpectedRevision, Body: input.Body},
		{ActorID: actor, Operation: ApplyRouteProposal, Path: input.Path, ExpectedRevision: input.ExpectedRevision, Body: input.Body},
		{ActorID: actor, Operation: input.Operation, Path: []PathComponent{{Name: "route_id", Value: "route-two"}}, ExpectedRevision: input.ExpectedRevision, Body: input.Body},
		{ActorID: actor, Operation: input.Operation, Path: input.Path, Body: input.Body},
		{ActorID: actor, Operation: input.Operation, Path: input.Path, ExpectedRevision: input.ExpectedRevision, Body: map[string]any{"first": []string{"b", "a"}, "second": 2}},
	}
	for i, candidate := range changed {
		hash, err := Fingerprint(candidate)
		if err != nil || hash == initial {
			t.Fatalf("changed command %d did not change fingerprint: %v", i, err)
		}
	}
}

func TestResultRejectsPrivateReplayFields(t *testing.T) {
	for _, body := range []string{
		`{"request_id":"transport"}`,
		`{"route":{"private_reference":"ticket"}}`,
		`{"routes":[{"share_token":"capability"}]}`,
		`{} {}`,
	} {
		if err := (Result{HTTPStatus: 200, ResponseBody: json.RawMessage(body)}).Validate(); err == nil {
			t.Fatalf("private response %s accepted", body)
		}
	}
	if err := (Result{HTTPStatus: 204, ResponseBody: json.RawMessage(`{}`)}).Validate(); err != nil {
		t.Fatal(err)
	}
}
