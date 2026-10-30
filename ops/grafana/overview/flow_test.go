package overview

import (
	"strings"
	"testing"

	"nomnom/ops/grafana/inventory"
)

// Sankey не строится с петлями, а record_fact — единственный переход без смены
// состояния. Поток идёт через все роли, фильтр $service его исказит, а increase()
// экстраполирует и даёт этапы больше начала.
func TestOrderFlowQuery(t *testing.T) {
	sc := Scope{Services: []string{inventory.Orders, inventory.OrdersWorker, inventory.OrdersConsumer}, AllServices: true}

	flow := orderFlow(sc)

	for _, want := range []string{
		`nomnom_fsm_trigger!="record_fact"`,
		`nomnom_fsm_result="applied"`,
		`job=~"nomnom/(orders|orders-worker|orders-consumer)"`,
		`offset $__range`,
	} {
		if !strings.Contains(flow, want) {
			t.Errorf("%s: нет %s", flow, want)
		}
	}
	for _, unwanted := range []string{"$service", "increase("} {
		if strings.Contains(flow, unwanted) {
			t.Errorf("%s: лишнее %s", flow, unwanted)
		}
	}
}

func TestOrderFlowIsOnlyInOrders(t *testing.T) {
	withAllPresent(t)
	for ctx, d := range render(t) {
		found := false
		for _, r := range d.Rows {
			if r.Title == "Order flow" {
				found = true
			}
		}
		if found != (ctx == "orders") {
			t.Errorf("%s: row Order flow = %v", ctx, found)
		}
	}
}

// Sankey строится из связей: узел один на состояние, иначе цепочка распадётся.
func TestSankeyOptionUsesNodesAndLinks(t *testing.T) {
	for _, want := range []string{`type: "sankey"`, "links", `labels.nomnom_fsm_from_state`, `labels.nomnom_fsm_to_state`} {
		if !strings.Contains(sankeyOption, want) {
			t.Errorf("нет %s", want)
		}
	}
}
