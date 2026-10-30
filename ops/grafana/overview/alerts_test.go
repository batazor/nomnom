package overview

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"nomnom/ops/grafana/inventory"
)

func allAlertGroups(t *testing.T) []AlertGroup {
	t.Helper()
	var groups []AlertGroup
	for _, o := range Services() {
		g, err := ServiceAlerts(o)
		if err != nil {
			t.Fatal(err)
		}
		groups = append(groups, g)
	}
	g, err := PlatformAlerts()
	if err != nil {
		t.Fatal(err)
	}
	return append(groups, g)
}

// Alert rule создаётся только на метриках «есть», как живая панель.
func TestAlertsFollowInventory(t *testing.T) {
	withStatus(t, inventory.Expected)
	for _, g := range allAlertGroups(t) {
		if len(g.Rules) > 0 {
			t.Errorf("%s: %d правил без метрик «есть»", g.Name, len(g.Rules))
		}
	}
	withAllPresent(t)
	uids := map[string]bool{}
	for _, g := range allAlertGroups(t) {
		if len(g.Rules) != len(g.Report) {
			t.Errorf("%s: %d из %d правил при полном инвентаре", g.Name, len(g.Rules), len(g.Report))
		}
		for _, r := range g.Rules {
			if uids[*r.Uid] {
				t.Errorf("uid %s повторяется", *r.Uid)
			}
			uids[*r.Uid] = true
		}
	}
}

// В артефактах нет UID datasource окружения: только placeholder и server-side expressions.
func TestAlertArtifactsHaveNoDatasourceUID(t *testing.T) {
	withAllPresent(t)
	for _, g := range allAlertGroups(t) {
		group, err := g.RuleGroup()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(group)
		if err != nil {
			t.Fatal(err)
		}
		for _, chunk := range strings.Split(string(raw), `"datasourceUid":"`)[1:] {
			uid, _, _ := strings.Cut(chunk, `"`)
			if uid != MetricsDatasource && uid != "__expr__" {
				t.Errorf("%s: datasourceUid %q", g.Name, uid)
			}
		}
	}
}

// Каждый alert ссылается на существующий раздел docs/runbooks.md.
func TestAlertRunbooksExist(t *testing.T) {
	doc, err := os.ReadFile("../../../docs/runbooks.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range allAlertGroups(t) {
		for _, r := range g.Rules {
			anchor := strings.TrimPrefix(r.Annotations["runbook_url"], runbookURL)
			if anchor == "" || !strings.Contains(string(doc), "\n## "+anchor+"\n") {
				t.Errorf("%s: нет раздела %q в docs/runbooks.md", r.Title, anchor)
			}
		}
	}
}
