package maxbot

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

//go:embed russian_trusted_root_ca_pem.crt
var trustedRoot []byte

const apiBaseURL = "https://platform-api2.max.ru"

type Update struct {
	Type      string `json:"update_type"`
	Timestamp int64  `json:"timestamp"`
	User      struct {
		ID int64 `json:"user_id"`
	} `json:"user"`
	Message struct {
		Sender struct {
			ID int64 `json:"user_id"`
		} `json:"sender"`
		Body struct {
			ID   string `json:"mid"`
			Text string `json:"text"`
		} `json:"body"`
	} `json:"message"`
	Callback struct {
		ID      string `json:"callback_id"`
		Payload string `json:"payload"`
		User    struct {
			ID int64 `json:"user_id"`
		} `json:"user"`
	} `json:"callback"`
}

type button struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Payload string `json:"payload,omitempty"`
	WebApp  string `json:"web_app,omitempty"`
}

type message struct {
	Text        string `json:"text"`
	Attachments []struct {
		Type    string `json:"type"`
		Payload struct {
			Buttons [][]button `json:"buttons"`
		} `json:"payload"`
	} `json:"attachments,omitempty"`
}

type Client struct {
	token    string
	username string
	baseURL  string
	http     *http.Client
	mu       sync.Mutex
	seen     map[string]time.Time
}

func NewClient(token, username string) (*Client, error) {
	if token == "" || username == "" {
		return nil, errors.New("MAX bot credentials are required")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system trust: %w", err)
	}
	if !roots.AppendCertsFromPEM(trustedRoot) {
		return nil, errors.New("invalid MAX trust root")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	return &Client{token: token, username: username, baseURL: apiBaseURL, http: &http.Client{Timeout: 8 * time.Second, Transport: transport}, seen: make(map[string]time.Time)}, nil
}

func (c *Client) Handle(ctx context.Context, update Update) error {
	var userID int64
	var key string
	var reply message
	switch update.Type {
	case "bot_started":
		userID = update.User.ID
		key = fmt.Sprintf("start:%d:%d", userID, update.Timestamp)
		reply = welcome()
	case "message_created":
		userID = update.Message.Sender.ID
		key = "message:" + update.Message.Body.ID
		reply = c.respond(update.Message.Body.Text)
	case "message_callback":
		userID = update.Callback.User.ID
		key = "callback:" + update.Callback.ID
		reply = c.respond(update.Callback.Payload)
	default:
		return nil
	}
	if userID <= 0 || key == "message:" || key == "callback:" || key == "start:0:0" {
		return errors.New("invalid MAX update")
	}
	if !c.claim(key) {
		return nil
	}
	if err := c.send(ctx, userID, reply); err != nil {
		c.release(key)
		return err
	}
	return nil
}

func (c *Client) claim(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for old, at := range c.seen {
		if now.Sub(at) > 10*time.Minute {
			delete(c.seen, old)
		}
	}
	if _, exists := c.seen[key]; exists {
		return false
	}
	c.seen[key] = now
	return true
}

func (c *Client) release(key string) {
	c.mu.Lock()
	delete(c.seen, key)
	c.mu.Unlock()
}

func welcome() message {
	m := message{Text: "Выберите настроение для дня в городе. Пока это демо: маршрут и точки вымышлены. Можно написать свой сценарий текстом."}
	m.addKeyboard([][]button{
		{{Type: "message", Text: "Вайб"}, {Type: "message", Text: "Настроение"}},
		{{Type: "message", Text: "Культура"}, {Type: "message", Text: "Энергия"}},
		{{Type: "message", Text: "Баланс"}, {Type: "message", Text: "Польза"}},
	})
	return m
}

func (c *Client) respond(input string) message {
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "/start" || text == "/help" || text == "/route" || text == "меню" || text == "другой сценарий" || text == "" {
		return welcome()
	}
	presets := map[string]string{"вайб": "vibe", "настроение": "mood", "культура": "culture", "энергия": "energy", "баланс": "balance", "польза": "benefit"}
	key, found := presets[text]
	var response string
	if found {
		response = "Готов пример маршрута «" + strings.TrimSpace(input) + "». Откройте его в Mini App. Все точки и время сейчас демонстрационные."
	} else {
		key = "custom"
		response = "Ваш сценарий получил. Персональный подбор пока не подключён, поэтому открываю общий демонстрационный маршрут. Ваш текст не превращён в реальные точки."
	}
	m := message{Text: response}
	m.addKeyboard([][]button{{{Type: "open_app", Text: "Открыть маршрут", WebApp: c.username, Payload: "demo_" + key}}, {{Type: "message", Text: "Другой сценарий"}}})
	return m
}

func (m *message) addKeyboard(rows [][]button) {
	m.Attachments = append(m.Attachments, struct {
		Type    string `json:"type"`
		Payload struct {
			Buttons [][]button `json:"buttons"`
		} `json:"payload"`
	}{Type: "inline_keyboard", Payload: struct {
		Buttons [][]button `json:"buttons"`
	}{Buttons: rows}})
}

func (c *Client) send(ctx context.Context, userID int64, body message) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	endpoint := c.baseURL + "/messages?user_id=" + url.QueryEscape(fmt.Sprint(userID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(encoded)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("send MAX message: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("send MAX message: HTTP %d", resp.StatusCode)
	}
	return nil
}
