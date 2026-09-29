package maxbot

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

type ScenarioRequest struct {
	PresetID   string
	SourceText string
}

func (c *Client) PrepareScenarioReply(update Update) (PreparedReply, *ScenarioRequest, bool, error) {
	reply, handled, err := c.PrepareReply(update)
	if err != nil || !handled {
		return reply, nil, handled, err
	}
	var input string
	switch update.Type {
	case "message_created":
		input = update.Message.Body.Text
	case "message_callback":
		input = update.Callback.Payload
	}
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "" || text == "/start" || text == "/help" || text == "/route" || text == "меню" || text == "другой сценарий" {
		body := welcome()
		body.Text = "Выберите тему дня или напишите свой сценарий. Старт и условия уточним в Mini App."
		reply.Body, err = json.Marshal(body)
		return reply, nil, true, err
	}
	if !utf8.ValidString(input) {
		return PreparedReply{}, nil, false, errors.New("invalid scenario text")
	}
	presets := map[string]string{"вайб": "vibe", "настроение": "mood", "культура": "culture", "энергия": "energy", "баланс": "balance", "польза": "benefit"}
	if preset, ok := presets[text]; ok {
		return reply, &ScenarioRequest{PresetID: preset}, true, nil
	}
	return reply, &ScenarioRequest{SourceText: input}, true, nil
}

func (c *Client) ScenarioReply(scenarioID string) (json.RawMessage, error) {
	body := message{Text: "Сценарий сохранён. Выберите старт и проверьте условия в Mini App — затем рассчитаем маршрут."}
	body.addKeyboard([][]button{
		{{Type: "open_app", Text: "Продолжить в Mini App", WebApp: c.username, Payload: "scenario_" + scenarioID}},
		{{Type: "message", Text: "Другой сценарий"}},
	})
	return json.Marshal(body)
}
