package overview

import (
	"strings"
	"testing"

	"nomnom/ops/grafana/inventory"
)

// Заказ перепрыгивает шаг, поэтому этап считается пересечением: прыжок
// confirmed → ready входит и в preparing, а поздний факт без перехода не входит.
func TestStageReachedCountsCrossingOfTheStage(t *testing.T) {
	sc := Scope{Services: []string{inventory.Orders, inventory.OrdersWorker, inventory.OrdersConsumer}, AllServices: true}

	preparing := stageReached(sc, 2)

	for _, want := range []string{
		`nomnom_fsm_from_state=~"placed|paid|confirmed"`,
		`nomnom_fsm_to_state=~"preparing|ready|picked_up|delivered|completed"`,
		`nomnom_fsm_result="applied"`,
		`job=~"nomnom/(orders|orders-worker|orders-consumer)"`,
		`[$__range]`,
		`offset $__range`,
	} {
		if !strings.Contains(preparing, want) {
			t.Errorf("%s: нет %s", preparing, want)
		}
	}
	if strings.Contains(preparing, "increase(") {
		t.Errorf("increase() экстраполирует, этап может стать больше начала воронки: %s", preparing)
	}
	if strings.Contains(preparing, "$service") {
		t.Errorf("воронка идёт через все роли, фильтр $service её исказит: %s", preparing)
	}
}

func TestOrderFunnelIsOnlyInOrdersAndUsesBars(t *testing.T) {
	withAllPresent(t)
	for ctx, d := range render(t) {
		found := false
		for _, r := range d.Rows {
			if r.Title == "Order funnel" {
				found = true
			}
		}
		if found != (ctx == "orders") {
			t.Errorf("%s: row Order funnel = %v", ctx, found)
		}
	}
	for _, p := range orderFunnel(Scope{Services: []string{inventory.Orders}, AllServices: true}) {
		if !p.Bars && !p.Sankey {
			t.Errorf("%s: воронка не на полосах и не Sankey", p.Title)
		}
	}
}

// Sankey не строится с петлями, а record_fact — единственный переход без смены состояния.
func TestOrderFlowHasNoLoops(t *testing.T) {
	flow := orderFlow(Scope{Services: []string{inventory.Orders}, AllServices: true})

	if !strings.Contains(flow, `nomnom_fsm_trigger!="record_fact"`) {
		t.Errorf("поток с петлями: %s", flow)
	}
}
