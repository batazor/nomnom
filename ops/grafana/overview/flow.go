package overview

import (
	"fmt"
	"strings"
)

const fsmTransitions = "nomnom_fsm_transitions_total"

// fsmFlow — переходы одного aggregate по парам состояний. loops — триггеры, которые не
// ведут вперёд (остаются в состоянии или возвращают назад): Sankey строится только без
// циклов.
func fsmFlow(sc Scope, aggregate string, loops ...string) string {
	return Sum("nomnom_fsm_from_state, nomnom_fsm_to_state", sc.Grown("$__range", fsmTransitions,
		fmt.Sprintf(`nomnom_fsm_aggregate_type=%q`, aggregate),
		`nomnom_fsm_result="applied"`,
		fmt.Sprintf(`nomnom_fsm_trigger!~%q`, strings.Join(loops, "|")))) + " > 0"
}

func flowPanel(title, what, expr string) Panel {
	return Panel{Title: title, Unit: "short", Sankey: true, Queries: []Query{{expr, ""}},
		Description: what + " между состояниями за выбранный диапазон: толщина — их число. " +
			"Панель плагина `volkovlabs-echarts-panel`: без него в stack Grafana покажет, что плагина нет. " +
			"Счётчики за диапазон: объект, созданный до него, может дойти до этапа внутри него, " +
			"а объект в работе ещё не дошёл, но и не потерян. Смотреть на диапазоне заметно длиннее обычного заказа."}
}

func orderFlowPanels(sc Scope) []Panel {
	return []Panel{flowPanel("Order flow", "Потоки заказов", fsmFlow(sc, "order", "record_fact"))}
}

func kitchenFlowPanels(sc Scope) []Panel {
	return []Panel{flowPanel("Kitchen flow", "Потоки заказов кухни", fsmFlow(sc, "restaurant_order", "report_delay"))}
}

// deliveryFlowPanels: снятие курьера возвращает Delivery в поиск и замкнуло бы цикл.
func deliveryFlowPanels(sc Scope) []Panel {
	return []Panel{flowPanel("Delivery steps", "Потоки доставок", fsmFlow(sc, "delivery", "unassign"))}
}
