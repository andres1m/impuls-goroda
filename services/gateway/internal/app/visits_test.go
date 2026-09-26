package app

import (
	"strings"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

func TestVisitInputValidation(t *testing.T) {
	reference := strings.Repeat("я", 2048)
	if err := validateParticipationInput(ParticipationInput{Action: "user_reported_confirmed", PrivateReference: &reference}); err != nil {
		t.Fatalf("valid Unicode reference rejected: %v", err)
	}
	reference += "я"
	if err := validateParticipationInput(ParticipationInput{Action: "user_reported_confirmed", PrivateReference: &reference}); err == nil {
		t.Fatal("oversized private reference accepted")
	}
	if err := validateParticipationInput(ParticipationInput{Action: "provider_confirmed"}); err == nil {
		t.Fatal("provider confirmation accepted through user action")
	}
	start := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	end := start.Add(-time.Minute)
	if err := validateExecutionInput(ExecutionInput{
		Status: domain.ExecutionCompleted, ConfirmationKind: domain.ConfirmationUserReported,
		ActualStartedAt: &start, ActualEndedAt: &end,
	}); err == nil {
		t.Fatal("reversed execution interval accepted")
	}
	if err := validateExecutionInput(ExecutionInput{
		Status: domain.ExecutionCompleted, ConfirmationKind: domain.ConfirmationProviderConfirmed,
	}); err == nil {
		t.Fatal("provider execution accepted through user action")
	}
}
