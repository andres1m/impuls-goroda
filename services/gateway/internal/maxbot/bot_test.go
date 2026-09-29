package maxbot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestPresetSendsAppButtonAndDeduplicates(t *testing.T) {
	var mu sync.Mutex
	var requests []message
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/messages" || r.URL.Query().Get("user_id") != "42" ||
			r.Header.Get("Authorization") != "test-token" {
			t.Errorf("unexpected MAX request: %s %s", r.Method, r.URL.String())
		}
		var body message
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode message: %v", err)
		}
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient("test-token", "example_bot")
	if err != nil {
		t.Fatal(err)
	}
	client.baseURL = server.URL
	client.http = server.Client()
	update := Update{Type: "message_created"}
	update.Message.Sender.ID = 42
	update.Message.Body.ID = "message-1"
	update.Message.Body.Text = "Культура"
	for range 2 {
		if err := client.Handle(context.Background(), &update); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("sent %d replies, want one", len(requests))
	}
	wantBtn := button{
		Type:    "open_app",
		Text:    "Открыть маршрут",
		WebApp:  "example_bot",
		Payload: "demo_culture",
	}
	if requests[0].Attachments[0].Payload.Buttons[0][0] != wantBtn {
		t.Fatalf("unexpected app button: %+v", requests[0].Attachments[0].Payload.Buttons[0][0])
	}
}

func TestWelcomeAndCustomTextAreExplicitlySynthetic(t *testing.T) {
	welcomeMessage := welcome()
	if len(welcomeMessage.Attachments[0].Payload.Buttons) != 3 {
		t.Fatal("preset buttons missing")
	}
	client := &Client{username: "example_bot"}
	custom := client.respond("Люблю прогулки и музыку")
	if custom.Attachments[0].Payload.Buttons[0][0].Payload != "demo_custom" {
		t.Fatal("custom preview payload missing")
	}
	if custom.Text == "" || custom.Text == "Люблю прогулки и музыку" {
		t.Fatal("custom input was treated as a real route")
	}
	if client.respond("Другой сценарий").Text != welcomeMessage.Text {
		t.Fatal("menu button did not return to preset selection")
	}
}
