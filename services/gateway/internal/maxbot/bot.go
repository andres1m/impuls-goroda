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
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed russian_trusted_root_ca_pem.crt
var trustedRoot []byte

const (
	apiBaseURL            = "https://platform-api2.max.ru"
	buttonTypeMessage     = "message"
	clientTimeout         = 8 * time.Second
	dedupTTL              = 10 * time.Minute
	maxResponseDrainBytes = 64 * 1024
)

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
	sends    sendThrottle
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
	var transport *http.Transport
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	return &Client{
		token:    token,
		username: username,
		baseURL:  apiBaseURL,
		http:     &http.Client{Timeout: clientTimeout, Transport: transport},
		seen:     make(map[string]time.Time),
	}, nil
}

type PreparedReply struct {
	UserID  int64
	EventID string
	Body    json.RawMessage
}

func (c *Client) PrepareReply(update Update) (PreparedReply, bool, error) {
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
		return PreparedReply{}, false, nil
	}
	if userID <= 0 || key == "message:" || key == "callback:" || key == "start:0:0" {
		return PreparedReply{}, false, errors.New("invalid MAX update")
	}
	if (update.Type == "bot_started" && update.Timestamp <= 0) || len(key) > 512 {
		return PreparedReply{}, false, errors.New("invalid MAX event identifier")
	}
	encoded, err := json.Marshal(reply)
	if err != nil {
		return PreparedReply{}, false, err
	}
	return PreparedReply{UserID: userID, EventID: key, Body: encoded}, true, nil
}

func (c *Client) SendReply(ctx context.Context, userID int64, raw json.RawMessage) error {
	var body message
	if userID <= 0 || json.Unmarshal(raw, &body) != nil || body.Text == "" {
		return errors.New("invalid MAX reply")
	}
	return c.send(ctx, userID, body)
}

func (c *Client) Handle(ctx context.Context, update *Update) error {
	if update == nil {
		return errors.New("invalid MAX update")
	}
	reply, handled, err := c.PrepareReply(*update)
	if err != nil {
		return err
	}
	if !handled || !c.claim(reply.EventID) {
		return nil
	}
	if err := c.SendReply(ctx, reply.UserID, reply.Body); err != nil {
		c.release(reply.EventID)
		return err
	}
	return nil
}

func (c *Client) claim(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for old, at := range c.seen {
		if now.Sub(at) > dedupTTL {
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
	m := message{
		Text: "Выберите настроение для дня в городе. Пока это демо: маршрут и точки вымышлены. Можно написать свой сценарий текстом.",
	}
	m.addKeyboard([][]button{
		{{Type: buttonTypeMessage, Text: "Вайб"}, {Type: buttonTypeMessage, Text: "Настроение"}},
		{{Type: buttonTypeMessage, Text: "Культура"}, {Type: buttonTypeMessage, Text: "Энергия"}},
		{{Type: buttonTypeMessage, Text: "Баланс"}, {Type: buttonTypeMessage, Text: "Польза"}},
	})
	return m
}

func (c *Client) respond(input string) message {
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "/start" || text == "/help" || text == "/route" || text == "меню" || text == "другой сценарий" ||
		text == "" {
		return welcome()
	}
	presets := map[string]string{
		"вайб":       "vibe",
		"настроение": "mood",
		"культура":   "culture",
		"энергия":    "energy",
		"баланс":     "balance",
		"польза":     "benefit",
	}
	key, found := presets[text]
	var response string
	if found {
		response = "Готов пример маршрута «" + strings.TrimSpace(
			input,
		) + "». Откройте его в Mini App. Все точки и время сейчас демонстрационные."
	} else {
		key = "custom"
		response = "Ваш сценарий получил. Персональный подбор пока не подключён, поэтому открываю общий демонстрационный маршрут. Ваш текст не превращён в реальные точки."
	}
	m := message{Text: response}
	m.addKeyboard(
		[][]button{
			{{Type: "open_app", Text: "Открыть маршрут", WebApp: c.username, Payload: "demo_" + key}},
			{{Type: buttonTypeMessage, Text: "Другой сценарий"}},
		},
	)
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

func (c *Client) send(ctx context.Context, userID int64, body message) (err error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal MAX message: %w", err)
	}
	endpoint := c.baseURL + "/messages?user_id=" + url.QueryEscape(strconv.FormatInt(userID, 10))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(encoded)))
	if err != nil {
		return fmt.Errorf("create MAX request: %w", err)
	}
	req.Header.Set("Authorization", c.token)
	req.Header.Set("Content-Type", "application/json")
	release, err := c.sends.acquire(ctx, userID)
	if err != nil {
		return err
	}
	defer release()
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("MAX message transport unavailable")
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close MAX response: %w", closeErr)
		}
	}()
	if _, copyErr := io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseDrainBytes)); copyErr != nil {
		return fmt.Errorf("drain MAX response: %w", copyErr)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("send MAX message: HTTP %d", resp.StatusCode)
	}
	return nil
}
