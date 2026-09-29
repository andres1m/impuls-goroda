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
	Resume     bool
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
	if text == "/continue" || text == "продолжить сценарий" {
		return reply, &ScenarioRequest{Resume: true}, true, nil
	}
	if text == "" || text == "/start" || text == "/help" || text == "/route" || text == "меню" || text == "другой сценарий" {
		reply.Body, err = c.ScenarioMenuReply()
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
	return c.scenarioReply(scenarioID, "Сценарий сохранён. Выберите старт и проверьте условия в Mini App — затем рассчитаем маршрут.")
}

func (c *Client) ExtractedScenarioReply(scenarioID string, extracted bool) (json.RawMessage, error) {
	text := "Текст сохранён. Не удалось предложить условия автоматически — укажите их в Mini App или выберите другую тему."
	if extracted {
		text = "Текст сохранён. Предложенные условия ждут вашей проверки в Mini App. Старт, дату и время укажите там — маршрут пока не рассчитан."
	}
	return c.scenarioReply(scenarioID, text)
}

func (c *Client) ScenarioMenuReply() (json.RawMessage, error) {
	body := welcome()
	body.Text = "Выберите тему дня или напишите свой сценарий. Старт и условия уточним в Mini App."
	body.addKeyboard([][]button{{{Type: "message", Text: "Продолжить сценарий"}}})
	return json.Marshal(body)
}

func (c *Client) ResumeScenarioReply(scenarioID string, completed bool) (json.RawMessage, error) {
	text := "Продолжите сохранённый сценарий в Mini App."
	if completed {
		text = "Откройте рассчитанные варианты в Mini App."
	}
	return c.scenarioReply(scenarioID, text)
}

func (c *Client) scenarioReply(scenarioID, text string) (json.RawMessage, error) {
	body := message{Text: text}
	body.addKeyboard([][]button{
		{{Type: "open_app", Text: "Продолжить в Mini App", WebApp: c.username, Payload: "scenario_" + scenarioID}},
		{{Type: "message", Text: "Другой сценарий"}},
	})
	return json.Marshal(body)
}
