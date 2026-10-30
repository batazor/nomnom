package overview

const fsmTransitions = "nomnom_fsm_transitions_total"

// orderFlow — переходы Order по парам состояний. Запись данных без смены состояния
// (record_fact) исключена: поток не имеет петель, иначе Sankey не построится.
func orderFlow(sc Scope) string {
	return Sum("nomnom_fsm_from_state, nomnom_fsm_to_state", sc.Grown("$__range", fsmTransitions,
		`nomnom_fsm_aggregate_type="order"`, `nomnom_fsm_result="applied"`, `nomnom_fsm_trigger!="record_fact"`)) + " > 0"
}

func orderFlowPanels(sc Scope) []Panel {
	return []Panel{
		{Title: "Order flow", Unit: "short", Sankey: true, Queries: []Query{{orderFlow(sc), ""}},
			Description: "Потоки заказов между состояниями за выбранный диапазон: толщина — число заказов. " +
				"Панель плагина `netsage-sankey-panel`: без него в stack Grafana покажет, что плагина нет. " +
				"Счётчики за диапазон: заказ, оформленный до него, может дойти до этапа внутри него, " +
				"а заказ в работе ещё не дошёл, но и не потерян. Смотреть на диапазоне заметно длиннее обычного заказа."},
	}
}
