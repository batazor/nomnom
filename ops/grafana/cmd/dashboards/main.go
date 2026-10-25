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
	for _, opts := range overview.Services() {
		d, err := overview.ServiceOverview(opts)
		if err != nil {
			fail(err)
		}
		var raw bytes.Buffer
		enc := json.NewEncoder(&raw)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(d.Manifest); err != nil {
			fail(err)
		}
		dir := filepath.Join("boundaries", opts.Context, "ops", "grafana")
		files[filepath.Join(dir, "dashboards", "overview.json")] = raw.Bytes()
		files[filepath.Join(dir, "README.md")] = []byte(overview.README(opts, d))
	}
	files[inventory.DocPath] = []byte(inventory.Markdown())

	switch {
	case *preview != "":
		for path, content := range files {
			if filepath.Ext(path) == ".json" {
				write(filepath.Join(*preview, filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(path)))))+".json"), content)
			}
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
