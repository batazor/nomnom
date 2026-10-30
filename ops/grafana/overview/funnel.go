package overview

import (
	"fmt"
	"strings"
)

// orderStages — путь Order к успешному итогу (docs/fsm.md#order), без placed: его
// считает nomnom_orders_placed_total, у размещения нет перехода FSM.
var orderStages = []string{"paid", "confirmed", "preparing", "ready", "picked_up", "delivered", "completed"}

const fsmTransitions = "nomnom_fsm_transitions_total"

// stageReached — сколько Order прошли этап за выбранный диапазон. Переходы монотонны
// и могут перепрыгнуть шаг, поэтому считается пересечение этапа: из любого состояния
// до него в него или дальше. Каждый Order пересекает этап один раз.
func stageReached(sc Scope, stage int) string {
	before := append([]string{"placed"}, orderStages[:stage]...)
	return round(Sum("", sc.Over("$__range", fsmTransitions,
		`nomnom_fsm_aggregate_type="order"`,
		`nomnom_fsm_result="applied"`,
		fmt.Sprintf(`nomnom_fsm_from_state=~%q`, strings.Join(before, "|")),
		fmt.Sprintf(`nomnom_fsm_to_state=~%q`, strings.Join(orderStages[stage:], "|")),
	)))
}

func round(expr string) string { return "round(" + expr + ")" }

func orderFunnel(sc Scope) []Panel {
	placed := round(Sum("", sc.Over("$__range", "nomnom_orders_placed_total")))
	counts := []Query{{placed, "placed"}}
	shares := []Query{{"100 * " + placed + " / " + placed, "placed"}}
	for i, stage := range orderStages {
		counts = append(counts, Query{stageReached(sc, i), stage})
		shares = append(shares, Query{"100 * " + stageReached(sc, i) + " / " + placed, stage})
	}
	window := "Счётчики за выбранный диапазон: заказ, оформленный до него, может дойти до этапа внутри него, " +
		"а заказ в работе ещё не дошёл, но и не потерян. Смотреть на диапазоне заметно длиннее обычного заказа."
	cancelled := round(Sum("nomnom_fsm_from_state", sc.Over("$__range", fsmTransitions,
		`nomnom_fsm_aggregate_type="order"`, `nomnom_fsm_result="applied"`, `nomnom_fsm_to_state="cancelled"`)))
	return []Panel{
		{Title: "Funnel: orders reaching the stage", Unit: "short", Bars: true, Queries: counts,
			Description: "Сколько заказов прошли каждый этап. " + window},
		{Title: "Funnel: % of placed", Unit: "percent", Bars: true, Max: 100, Queries: shares,
			Description: "Доля оформленных заказов, прошедших этап; последняя полоса — конверсия до завершения. " + window},
		{Title: "Lost: state before cancellation", Unit: "short", Bars: true,
			Queries:     []Query{{cancelled, "{{nomnom_fsm_from_state}}"}},
			Description: "Из какого состояния заказ ушёл в `cancelled`: `rejected` и `delivery_failed` — итог соседа, остальное — отмена или таймаут на этом шаге."},
		{Title: "Lost: reason", Unit: "short", Bars: true, Queries: []Query{
			{round(Sum("nomnom_reason", sc.Over("$__range", "nomnom_orders_cancelled_total"))), "cancelled: {{nomnom_reason}}"},
			{round(Sum("nomnom_reason", sc.Over("$__range", "nomnom_orders_failed_total"))), "failed: {{nomnom_reason}}"},
		}, Description: "Причины отрицательного итога за выбранный диапазон."},
	}
}
