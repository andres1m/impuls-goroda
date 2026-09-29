package app

import (
	"strings"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
)

func scenarioGuidance(scenario routewire.BotScenario) string {
	input := scenario.Input
	var missing []string
	if input.Origin == nil || input.City == nil || input.Timezone == nil {
		missing = append(missing, "старт на карте")
	}
	if input.StartAt == nil || input.EndAt == nil {
		missing = append(missing, "дату и время начала и завершения")
	}
	constraints := input.Constraints
	if constraints == nil || constraints.InterestMask == nil {
		missing = append(missing, "интересы (можно оставить без предпочтений)")
	}
	if constraints == nil || constraints.MovementModes == nil || len(*constraints.MovementModes) == 0 {
		missing = append(missing, "способ передвижения")
	}
	if constraints == nil || constraints.LoadProfile == nil {
		missing = append(missing, "темп")
	}
	if constraints == nil || constraints.Budget == nil || constraints.Budget.Mode == nil {
		missing = append(missing, "условие бюджета")
	} else if *constraints.Budget.Mode != "none" && constraints.Budget.Limit == nil {
		missing = append(missing, "сумму бюджета")
	}
	text := "Продолжите сохранённые условия в Mini App."
	if len(missing) > 0 {
		text += "\nЕщё нужно указать:\n• " + strings.Join(missing, "\n• ")
	}
	if scenario.PendingExtraction != nil {
		text += "\nПредложенные по вашему тексту условия требуют проверки — перенесите их или заполните форму самостоятельно."
	}
	if scenario.Outcome != nil {
		text += "\nПредыдущий расчёт не подобрал маршрут. Измените условия перед новым расчётом."
	}
	return text + "\nПроверьте все условия и подтвердите расчёт в Mini App."
}
