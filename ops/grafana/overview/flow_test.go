package overview

import (
	"encoding/json"
	"strings"
	"testing"

	"nomnom/ops/grafana/inventory"
)

// Sankey не строится с циклами. Поток идёт через все роли, фильтр $service его исказит,
// а increase() экстраполирует и даёт этапы больше начала.
func TestFlowQueries(t *testing.T) {
	sc := Scope{Services: []string{inventory.Orders, inventory.OrdersWorker, inventory.OrdersConsumer}, AllServices: true}
	for name, tc := range map[string]struct {
		panels []Panel
		want   []string
	}{
		"orders":      {orderFlowPanels(sc), []string{`nomnom_fsm_aggregate_type="order"`, `nomnom_fsm_trigger!~"record_fact"`}},
		"restaurants": {kitchenFlowPanels(sc), []string{`nomnom_fsm_aggregate_type="restaurant_order"`, `nomnom_fsm_trigger!~"report_delay"`}},
		"delivery":    {deliveryFlowPanels(sc), []string{`nomnom_fsm_aggregate_type="delivery"`, `nomnom_fsm_trigger!~"unassign"`}},
	} {
		flow := tc.panels[0].Queries[0].Expr
		for _, want := range append(tc.want, `nomnom_fsm_result="applied"`, `offset $__range`) {
			if !strings.Contains(flow, want) {
				t.Errorf("%s: %s: нет %s", name, flow, want)
			}
		}
		for _, unwanted := range []string{"$service", "increase("} {
			if strings.Contains(flow, unwanted) {
				t.Errorf("%s: %s: лишнее %s", name, flow, unwanted)
			}
		}
	}
}

func TestFlowRowIsInContextsWithAProcess(t *testing.T) {
	withAllPresent(t)
	want := map[string]bool{"orders": true, "restaurants": true, "delivery": true}
	for ctx, d := range render(t) {
		found := false
		for _, r := range d.Rows {
			if r.Title == "Flow" {
				found = true
			}
		}
		if found != want[ctx] {
			t.Errorf("%s: row Flow = %v", ctx, found)
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

// Product — первая вкладка там, где есть процесс или доменные метрики; все панели в auto
// grid, скрываются без данных, а панелей, ждущих метрик, в dashboard нет.
func TestDashboardsUseTabsAutoGridAndShowRules(t *testing.T) {
	withAllPresent(t)
	for ctx, d := range render(t) {
		raw, err := json.Marshal(d.Manifest)
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		if strings.Contains(s, `"GridLayout"`) {
			t.Errorf("%s: панели не в auto grid", ctx)
		}
		if !strings.Contains(s, `"ConditionalRenderingData"`) {
			t.Errorf("%s: нет правила скрытия панелей без данных", ctx)
		}
		if ctx == "bff" {
			continue
		}
		product, technical := strings.Index(s, `"title":"Product"`), strings.Index(s, `"title":"Technical"`)
		if product < 0 || technical < product {
			t.Errorf("%s: вкладки Product %d, Technical %d", ctx, product, technical)
		}
	}
}

func TestPanelsWaitingForMetricsAreNotInTheDashboard(t *testing.T) {
	withStatus(t, inventory.Expected)
	for ctx, d := range render(t) {
		raw, err := json.Marshal(d.Manifest)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "TODO") {
			t.Errorf("%s: TODO-панель в dashboard", ctx)
		}
	}
}
