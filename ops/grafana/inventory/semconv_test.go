package inventory

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Registry ops/semconv — источник OTel-имён, instruments, units и attributes,
// которые проверяет Weaver (make semconv-check); инвентарь — их имена в Mimir.
const semconvModel = "../../semconv/model"

type semconvGroup struct {
	Type       string `yaml:"type"`
	MetricName string `yaml:"metric_name"`
	Instrument string `yaml:"instrument"`
	Unit       string `yaml:"unit"`
	Attributes []struct {
		Ref string `yaml:"ref"`
	} `yaml:"attributes"`
}

// promName — OTLP → Prometheus (UnderscoreEscapingWithSuffixes): точки → '_',
// unit s → _seconds, annotation unit {…} не добавляется, counter → _total.
func promName(g semconvGroup) string {
	name := strings.ReplaceAll(g.MetricName, ".", "_")
	if g.Unit == "s" {
		name += "_seconds"
	}
	if g.Instrument == "counter" {
		name += "_total"
	}
	return name
}

var instrumentType = map[string]Type{"counter": Counter, "histogram": Histogram, "gauge": Gauge}

func loadSemconv(t *testing.T) map[string]semconvGroup {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(semconvModel, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("registry %s: %v", semconvModel, err)
	}
	metrics := map[string]semconvGroup{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Groups []semconvGroup `yaml:"groups"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, g := range doc.Groups {
			if g.Type == "metric" {
				metrics[promName(g)] = g
			}
		}
	}
	return metrics
}

// Каждая собственная метрика инвентаря описана в registry с тем же типом и
// labels, и registry не описывает метрик вне инвентаря.
func TestInventoryMatchesSemconvRegistry(t *testing.T) {
	registry := loadSemconv(t)
	seen := map[string]bool{}
	for _, m := range Metrics {
		if !strings.HasPrefix(m.Name, "nomnom_") {
			continue
		}
		seen[m.Name] = true
		g, ok := registry[m.Name]
		if !ok {
			t.Errorf("%s: нет в ops/semconv", m.Name)
			continue
		}
		if instrumentType[g.Instrument] != m.Type {
			t.Errorf("%s: тип %s в инвентаре, %s в registry", m.Name, m.Type, g.Instrument)
		}
		var labels []string
		for _, a := range g.Attributes {
			labels = append(labels, strings.ReplaceAll(a.Ref, ".", "_"))
		}
		want := slices.Clone(m.Labels)
		slices.Sort(labels)
		slices.Sort(want)
		if !slices.Equal(labels, want) {
			t.Errorf("%s: labels %v в инвентаре, %v в registry", m.Name, want, labels)
		}
	}
	for name := range registry {
		if !seen[name] {
			t.Errorf("%s: в ops/semconv, но не в инвентаре", name)
		}
	}
}
