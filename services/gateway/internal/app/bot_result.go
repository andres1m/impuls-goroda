package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/google/uuid"
)

func (h *BotHandler) scenarioResultReply(ctx context.Context, q *postgres.Queries, owner d.UserID, scenario routewire.BotScenario) (json.RawMessage, error) {
	if scenario.Outcome == nil {
		return h.client.ResultScenarioReply(scenario.ScenarioID, "Маршрут ещё не рассчитан. Завершите ввод и подтвердите условия в Mini App.", "Продолжить ввод")
	}
	outcome := scenario.Outcome
	mode := map[string]string{"live": "актуальные", "prepared": "подготовленные", "synthetic": "демонстрационные"}[outcome.DataMode]
	if mode == "" {
		return nil, errors.New("invalid scenario result mode")
	}
	var text strings.Builder
	if outcome.Status == "NO_FEASIBLE_ROUTE" || outcome.Status == "CONFLICT" {
		text.WriteString("Не удалось подобрать маршрут с этими условиями.\n")
		for i, conflict := range outcome.Conflicts {
			if i == 3 {
				break
			}
			fmt.Fprintf(&text, "• %s\n", previewText(conflict.Message, 200))
		}
		for i, warning := range outcome.Warnings {
			if i == 3 {
				break
			}
			fmt.Fprintf(&text, "• %s\n", previewText(warning.Message, 200))
		}
		fmt.Fprintf(&text, "\nДанные последнего расчёта: %s. Условия сохранены — их можно исправить в Mini App.", mode)
		return h.client.ResultScenarioReply(scenario.ScenarioID, text.String(), "Исправить условия")
	}
	if outcome.Status != "READY" && outcome.Status != "PARTIAL" {
		return nil, errors.New("invalid scenario result status")
	}
	var summaries []string
	missing := 0
	for _, routeID := range outcome.RouteIDs {
		id, err := uuid.Parse(routeID)
		if err != nil {
			return nil, errors.New("invalid scenario route identifier")
		}
		route, err := q.ReadOwnerRoute(ctx, d.RouteID(id), owner)
		if errors.Is(err, postgres.ErrNotFound) {
			missing++
			continue
		}
		if err != nil {
			return nil, err
		}
		summary, err := routePreview(route)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	if len(summaries) == 0 {
		return h.client.ResultScenarioReply(scenario.ScenarioID, "Сохранённые варианты этого расчёта больше недоступны. Можно начать другой сценарий в меню бота.", "")
	}
	fmt.Fprintf(&text, "Сохранённые варианты: %d\nДанные последнего расчёта: %s.\n", len(summaries), mode)
	if outcome.Status == "PARTIAL" || len(outcome.Warnings) != 0 {
		text.WriteString("У расчёта есть предупреждения — проверьте их в Mini App.\n")
	}
	for i, summary := range summaries {
		fmt.Fprintf(&text, "\n%d. %s\n", i+1, summary)
	}
	if missing > 0 {
		text.WriteString("\nЧасть прежних вариантов больше недоступна.\n")
	}
	text.WriteString("\nЭто снимок сохранённых планов на момент запроса. Расписание и условия доступа могут измениться; перед выходом проверьте маршрут в Mini App.")
	return h.client.ResultScenarioReply(scenario.ScenarioID, text.String(), "Открыть варианты")
}

func routePreview(route routewire.OwnerRoute) (string, error) {
	plan := route.Plan
	zone, err := time.LoadLocation(plan.Timezone)
	if err != nil || !plan.EndAt.After(plan.StartAt) || plan.StartAt.IsZero() ||
		(route.Lifecycle != "draft" && route.Lifecycle != "saved") ||
		(plan.Result != "READY" && plan.Result != "PARTIAL") || len(plan.Conflicts) != 0 {
		return "", errors.New("invalid saved route preview")
	}
	personal, err := previewMoney(plan.Cost.KnownPersonal)
	if err != nil {
		return "", err
	}
	transport, err := previewMoney(plan.Cost.KnownTransport)
	if err != nil {
		return "", err
	}
	title := map[string]string{"urban_avantgarde": "Современный город", "history_heritage": "История и культура", "action_social": "Движение и польза"}[plan.ArchetypeID]
	if title == "" {
		title = "Маршрут"
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%s\nОкно: %s — %s (местное время).\n", title, plan.StartAt.In(zone).Format("02.01.2006 15:04"), plan.EndAt.In(zone).Format("02.01.2006 15:04"))
	visits := 0
	var names []string
	prepared, synthetic := false, false
	for _, step := range plan.Steps {
		if step.Catalog == nil {
			continue
		}
		visits++
		if len(names) < 3 {
			names = append(names, previewText(step.Catalog.Title, 80))
		}
		prepared = prepared || step.Catalog.DataMode == "prepared"
		synthetic = synthetic || step.Catalog.DataMode == "synthetic"
	}
	fmt.Fprintf(&text, "Посещений: %d", visits)
	if len(names) > 0 {
		fmt.Fprintf(&text, " · %s", strings.Join(names, " → "))
		if visits > len(names) {
			text.WriteString(" → …")
		}
	}
	fmt.Fprintf(&text, "\nИзвестные личные расходы: %s (включая транспорт: %s).\n", personal, transport)
	if len(plan.Cost.UnknownComponents) > 0 {
		text.WriteString("Часть расходов неизвестна; это не итоговая стоимость.\n")
	}
	if synthetic {
		text.WriteString("В этом плане есть демонстрационные данные.\n")
	} else if prepared {
		text.WriteString("В этом плане есть подготовленные данные.\n")
	}
	for _, leg := range plan.Legs {
		if leg.Verification != "verified" {
			text.WriteString("Часть переходов не проверена или рассчитана приблизительно.\n")
			break
		}
	}
	for _, issue := range route.Issues {
		if issue.State != "resolved" {
			text.WriteString("Есть изменения или проблемы маршрута — проверьте их в Mini App.\n")
			break
		}
	}
	if plan.Result == "PARTIAL" || len(plan.Warnings) > 0 {
		text.WriteString("Есть предупреждения по этому варианту.\n")
	}
	return strings.TrimSpace(text.String()), nil
}

func previewMoney(value routewire.Money) (string, error) {
	amount, err := strconv.ParseUint(value.AmountMinor, 10, 63)
	if err != nil || len(value.Currency) != 3 {
		return "", errors.New("invalid saved route cost")
	}
	for _, letter := range value.Currency {
		if letter < 'A' || letter > 'Z' {
			return "", errors.New("invalid saved route currency")
		}
	}
	if value.Currency != "RUB" {
		return "сумма в " + value.Currency + " — в Mini App", nil
	}
	return fmt.Sprintf("%d.%02d ₽", amount/100, amount%100), nil
}

func previewText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit]) + "…"
}
