package overview

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"nomnom/ops/grafana/inventory"
)

// withAllPresent проверяет живой путь генератора: все метрики в статусе «есть».
func withAllPresent(t *testing.T) {
	t.Helper()
	withStatus(t, inventory.Present)
}

// withStatus временно выставляет всем метрикам один статус.
func withStatus(t *testing.T, status inventory.Status) {
	t.Helper()
	saved := make([]map[string]inventory.Status, len(inventory.Metrics))
	for i, m := range inventory.Metrics {
		saved[i] = m.Status
		live := map[string]inventory.Status{}
		for s := range m.Status {
			live[s] = status
		}
		inventory.Metrics[i].Status = live
	}
	t.Cleanup(func() {
		for i := range inventory.Metrics {
			inventory.Metrics[i].Status = saved[i]
		}
	})
}

func render(t *testing.T) map[string]Dashboard {
	t.Helper()
	out := map[string]Dashboard{}
	for _, o := range Services() {
		d, err := ServiceOverview(o)
		if err != nil {
			t.Fatal(err)
		}
		out[o.Context] = d
	}
	return out
}

func TestUIDsAreStable(t *testing.T) {
	for ctx, d := range render(t) {
		if d.UID != "nomnom-"+ctx+"-overview" || d.Manifest.Metadata.Name != d.UID {
			t.Errorf("%s: uid %q, name %q", ctx, d.UID, d.Manifest.Metadata.Name)
		}
		if d.Manifest.ApiVersion != "dashboard.grafana.app/v2" {
			t.Errorf("%s: apiVersion %s", ctx, d.Manifest.ApiVersion)
		}
	}
}

// Порядок rows задан кодом: у сервисов отличается только набор включённых rows.
func TestRowOrderIsShared(t *testing.T) {
	order := []string{"Service health", "Runtime", "Pods", "Dependencies", "Kafka", "Use cases", "Transactions", "Domain Lifecycle", "Domain metrics", "Temporal", "Kitchen", "Authorization", "Certificates"}
	for ctx, d := range render(t) {
		last := -1
		for _, r := range d.Rows {
			i := slices.Index(order, r.Title)
			if i <= last {
				t.Errorf("%s: row %q вне общего порядка", ctx, r.Title)
			}
			last = i
		}
		if d.Rows[0].Title != "Service health" {
			t.Errorf("%s: первая row %q", ctx, d.Rows[0].Title)
		}
	}
}

func TestNoPanelIsLiveWithoutPresentMetrics(t *testing.T) {
	withStatus(t, inventory.Expected)
	for ctx, d := range render(t) {
		for _, r := range d.Rows {
			for _, p := range r.Panels {
				if p.Live {
					t.Errorf("%s/%s: живая панель без метрик «есть»", ctx, p.Title)
				}
			}
		}
	}
}

func TestAllPanelsGoLiveWhenInventoryIsPresent(t *testing.T) {
	withAllPresent(t)
	for ctx, d := range render(t) {
		for _, r := range d.Rows {
			for _, p := range r.Panels {
				if !p.Live || len(p.Missing) > 0 {
					t.Errorf("%s/%s: не стала живой, missing %v", ctx, p.Title, p.Missing)
				}
			}
		}
	}
}

func TestDeploymentAnnotationIsValidPromQL(t *testing.T) {
	a, err := Analyze(DeploymentAnnotation(Scope{Var: "service"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Selectors) != 2 || a.Selectors[0].Series != "target_info" {
		t.Fatalf("selectors %+v", a.Selectors)
	}
}

func TestArtifactsHaveNoEnvironmentReferencesOrSecrets(t *testing.T) {
	withAllPresent(t)
	for ctx, d := range render(t) {
		raw, err := json.Marshal(d.Manifest)
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		var refs []struct {
			Name string `json:"name"`
		}
		for _, chunk := range strings.Split(s, `"datasource":{"name":"`)[1:] {
			name, _, _ := strings.Cut(chunk, `"`)
			refs = append(refs, struct {
				Name string `json:"name"`
			}{name})
		}
		for _, r := range refs {
			if !slices.Contains([]string{"${metrics}", "-- Grafana --"}, r.Name) {
				t.Errorf("%s: datasource reference %q не variable", ctx, r.Name)
			}
		}
		for _, bad := range []string{"password", "token", "apiKey", "secret", "localhost", "namespace\":\""} {
			if strings.Contains(strings.ToLower(s), strings.ToLower(bad)) {
				t.Errorf("%s: артефакт содержит %q", ctx, bad)
			}
		}
	}
}

func TestInventoryHasNoForbiddenLabels(t *testing.T) {
	for _, m := range inventory.Metrics {
		for _, l := range m.Labels {
			if slices.Contains(inventory.ForbiddenLabels, l) {
				t.Errorf("%s: label %s", m.Name, l)
			}
		}
		if _, ok := inventory.Lookup(m.Name); !ok {
			t.Errorf("%s: lookup", m.Name)
		}
		if !slices.ContainsFunc(inventory.Sections, func(s inventory.Section) bool { return s.ID == m.Section }) {
			t.Errorf("%s: section %s", m.Name, m.Section)
		}
	}
}

func TestPlatformPanelsFollowInventory(t *testing.T) {
	for _, tc := range []struct {
		status inventory.Status
		live   bool
	}{{inventory.Present, true}, {inventory.Expected, false}} {
		withStatus(t, tc.status)
		d, err := PlatformOverview()
		if err != nil {
			t.Fatal(err)
		}
		if d.UID != PlatformUID || d.Manifest.Metadata.Name != PlatformUID {
			t.Errorf("uid %q, name %q", d.UID, d.Manifest.Metadata.Name)
		}
		for _, r := range d.Rows {
			for _, p := range r.Panels {
				if p.Live != tc.live {
					t.Errorf("%s/%s: live=%v при статусе %s", r.Title, p.Title, p.Live, tc.status)
				}
			}
		}
	}
}

// Метрики инфраструктуры без target_info: join с ним сделал бы панели пустыми.
func TestInfraScopeHasNoTargetInfoJoin(t *testing.T) {
	sc := servicePods(Options{Context: "orders", Services: []string{inventory.Orders}})
	expr := Sum("pod", sc.Rate("container_cpu_usage_seconds_total"))
	if strings.Contains(expr, "target_info") || !strings.Contains(expr, `job="infra/kubernetes"`) || !strings.Contains(expr, `namespace="nomnom-orders"`) {
		t.Errorf("expr %s", expr)
	}
}

func TestGeneratorRejectsUninventoriedMetricAndForbiddenLabel(t *testing.T) {
	g := &generator{opts: Options{}}
	sc := Scope{Var: "service", Services: []string{inventory.Orders}}
	g.check("p", Query{Expr: Sum("order_id", sc.Rate("nomnom_orders_placed_total"))}, sc)
	g.check("p", Query{Expr: Sum("", sc.Rate("unknown_metric_total"))}, sc)
	joined := strings.Join(g.errs, "\n")
	for _, want := range []string{"high-cardinality label order_id", "unknown_metric_total отсутствует в инвентаре"} {
		if !strings.Contains(joined, want) {
			t.Errorf("нет ошибки %q в %q", want, joined)
		}
	}
}

func TestGeneratorRejectsTraceLabelOutsideSeries(t *testing.T) {
	g := &generator{opts: Options{}}
	sc := Scope{Var: "service", Services: []string{inventory.Orders}}
	q := Query{Expr: Sum("rpc_method", sc.Rate(rpcServer+"_count"))}
	g.checkTrace("p", q, spanLabel("rpc_method"))
	if len(g.errs) > 0 {
		t.Fatalf("label серии отклонён: %v", g.errs)
	}
	g.checkTrace("p", q, spanLabel("db_operation_name")+` && span.x="${__field.labels.server_address}"`)
	joined := strings.Join(g.errs, "\n")
	for _, want := range []string{"label db_operation_name не входит в группировку", "label server_address без span attribute"} {
		if !strings.Contains(joined, want) {
			t.Errorf("нет ошибки %q в %q", want, joined)
		}
	}
}
