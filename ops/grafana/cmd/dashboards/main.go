// Command dashboards генерирует overview-dashboards, README и инвентарь метрик.
//
//	go run ./cmd/dashboards -root ../..           # записать артефакты
//	go run ./cmd/dashboards -root ../.. -check    # CI: упасть при расхождении
//	go run ./cmd/dashboards -preview DIR          # все метрики «есть»: живые панели для проверки в Grafana
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"nomnom/ops/grafana/inventory"
	"nomnom/ops/grafana/overview"
)

func main() {
	root := flag.String("root", "../..", "корень репозитория")
	check := flag.Bool("check", false, "сравнить с закоммиченными файлами вместо записи")
	preview := flag.String("preview", "", "каталог для preview-варианта со всеми метриками в статусе «есть»")
	flag.Parse()

	if *preview != "" {
		for i := range inventory.Metrics {
			for s := range inventory.Metrics[i].Status {
				inventory.Metrics[i].Status[s] = inventory.Present
			}
		}
	}

	files := map[string][]byte{}
	// previews — имя файла preview-варианта для каждого dashboard.
	previews := map[string]string{}
	for _, opts := range overview.Services() {
		d, err := overview.ServiceOverview(opts)
		if err != nil {
			fail(err)
		}
		dir := filepath.Join("boundaries", opts.Context, "ops", "grafana")
		path := filepath.Join(dir, "dashboards", "overview.json")
		files[path] = encode(d)
		previews[path] = opts.Context + ".json"
		alerts, err := overview.ServiceAlerts(opts)
		if err != nil {
			fail(err)
		}
		files[filepath.Join(dir, "alerts", "rules.json")] = encodeAlerts(alerts)
		files[filepath.Join(dir, "README.md")] = []byte(overview.README(opts, d, alerts))
	}
	platform, err := overview.PlatformOverview()
	if err != nil {
		fail(err)
	}
	files[overview.PlatformPath] = encode(platform)
	previews[overview.PlatformPath] = "platform.json"
	platformAlerts, err := overview.PlatformAlerts()
	if err != nil {
		fail(err)
	}
	files[overview.PlatformAlertsPath] = encodeAlerts(platformAlerts)
	files[filepath.Join(filepath.Dir(overview.PlatformPath), "README.md")] = []byte(overview.PlatformREADME(platform, platformAlerts))
	files[inventory.DocPath] = []byte(inventory.Markdown())

	switch {
	case *preview != "":
		for path, name := range previews {
			write(filepath.Join(*preview, name), files[path])
		}
	case *check:
		stale := 0
		for path, content := range files {
			current, err := os.ReadFile(filepath.Join(*root, path))
			if err != nil || !bytes.Equal(current, content) {
				fmt.Fprintf(os.Stderr, "устарел: %s\n", path)
				stale++
			}
		}
		if stale > 0 {
			fail(fmt.Errorf("%d файлов не совпадают с генератором: выполните make grafana-generate", stale))
		}
	default:
		for path, content := range files {
			write(filepath.Join(*root, path), content)
		}
	}
}

func encodeAlerts(a overview.AlertGroup) []byte {
	group, err := a.RuleGroup()
	if err != nil {
		fail(err)
	}
	return encodeJSON(group)
}

func encode(d overview.Dashboard) []byte { return encodeJSON(d.Manifest) }

func encodeJSON(v any) []byte {
	var raw bytes.Buffer
	enc := json.NewEncoder(&raw)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fail(err)
	}
	return raw.Bytes()
}

func write(path string, content []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fail(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
