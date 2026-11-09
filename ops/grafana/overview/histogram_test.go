package overview

import (
	"strings"
	"testing"

	"nomnom/ops/grafana/inventory"
)

// AC-1093: суффикс у native histogram — ошибка генерации; у классической — нет.
func TestGeneratorRejectsSuffixOfNativeHistogram(t *testing.T) {
	g := &generator{opts: Options{}}
	sc := Scope{Var: "service", Services: []string{inventory.OrdersConsumer}}
	g.check("p", Query{Expr: Sum("", sc.Rate("watermill_consumer_handler_execution_time_seconds_count"))}, sc)
	if len(g.errs) > 0 {
		t.Fatalf("суффикс классической histogram отклонён: %v", g.errs)
	}
	g.check("p", Query{Expr: Sum("", sc.Rate(rpcServer+"_count"))}, sc)
	joined := strings.Join(g.errs, "\n")
	if !strings.Contains(joined, "p: rpc_server_call_duration_seconds_count — native histogram") {
		t.Errorf("нет ошибки суффикса native histogram в %q", joined)
	}
}

// AC-1096: порог latency SLO не степень двойки — ошибка генерации dashboard и alerts.
func TestLatencyThresholdMustBePowerOfTwo(t *testing.T) {
	for _, o := range Services() {
		if o.Context != "orders" {
			continue
		}
		o.SLO = &SLO{Availability: o.SLO.Availability, Latency: &LatencySLO{Threshold: "0.1", Target: 0.95}}
		_, dashErr := ServiceOverview(o)
		_, alertErr := ServiceAlerts(o)
		for _, err := range []error{dashErr, alertErr} {
			if err == nil || !strings.Contains(err.Error(), "orders: порог latency SLO 0.1 s — не степень двойки") {
				t.Errorf("ошибка %v, ожидался отказ порога 0.1", err)
			}
		}
		return
	}
	t.Fatal("нет Options orders")
}

// AC-1097: latency SLI orders — histogram_fraction по native histogram с порогом 0.25.
func TestOrdersLatencySLIUsesHistogramFraction(t *testing.T) {
	for _, o := range Services() {
		if o.Context != "orders" {
			continue
		}
		alerts := o.sloAlerts(Scope{Services: o.Services, Literal: true}, UID(o.Context))
		var latency []string
		for _, a := range alerts {
			if strings.Contains(a.Slug, "slo-latency") {
				latency = append(latency, a.Value)
			}
		}
		if len(latency) == 0 {
			t.Fatalf("нет latency SLO alerts среди %d", len(alerts))
		}
		for _, v := range latency {
			if !strings.Contains(v, "1 - histogram_fraction(0, 0.25, sum by (job) (increase(rpc_server_call_duration_seconds{") {
				t.Errorf("latency SLI не через histogram_fraction: %s", v)
			}
			if strings.Contains(v, "_bucket") || strings.Contains(v, "_count") {
				t.Errorf("latency SLI с суффиксом histogram: %s", v)
			}
		}
		return
	}
	t.Fatal("нет Options orders")
}
