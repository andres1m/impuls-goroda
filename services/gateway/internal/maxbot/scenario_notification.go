package maxbot

import (
	"context"

	"github.com/google/uuid"
)

func (c *Client) SendScenarioResult(ctx context.Context, userID int64, scenarioID uuid.UUID, text string) (NotificationReceipt, error) {
	if c == nil || c.username == "" || scenarioID.Version() != 7 || scenarioID.Variant() != uuid.RFC4122 {
		return NotificationReceipt{}, &NotificationSendError{Kind: "invalid_input"}
	}
	content := message{Text: text}
	content.addKeyboard([][]button{{{Type: "open_app", Text: "Открыть результат", WebApp: c.username, Payload: "scenario_" + scenarioID.String()}}})
	return c.sendNotification(ctx, userID, content)
}
