package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

const adrIndexPath = "docs/adr/README.md"

var gatewayEdge = regexp.MustCompile(`(?m)^\s*nomnom\.cell1\.internalGateway -\[grpc\]-> nomnom\.cell1\.(\w+)\.api\b`)

// kindValues — release chart'ов kind, чьё имя не совпадает с заголовком
// процесса в модели.
var kindValues = map[string]string{
	"bff":          "Cosmo Router + plugin BFF",
	"ui":           "Customer UI",
	"controlplane": "Control plane",
}

// checkModel находит расхождения модели с ops: topics, маршруты internal
// gateway и запускаемые процессы, а также ADR без ссылки в индексе. Потребители topics здесь не сверяются с кодом:
// подписки объявлены на четырёх языках.
func checkModel(root, model string, topics []Topic, consumers map[string][]string) ([]string, error) {
	var problems []string

	known := map[string]bool{}
	for _, t := range topics {
		known[short(t.Name)] = true
		if !strings.HasSuffix(t.Name, dlqSuffix) && !strings.Contains(model, short(t.Name)) {
			problems = append(problems, "topic "+t.Name+" нет в model.c4")
		}
	}
	for name := range consumers {
		if !known[name] {
			problems = append(problems, "model.c4: потребитель читает неизвестный topic "+name)
		}
	}

	backends, err := readRouteBackends(filepath.Join(root, "ops/kind/manifests/gateway/gateway.yaml"))
	if err != nil {
		return nil, err
	}
	var edges []string
	for _, m := range gatewayEdge.FindAllStringSubmatch(model, -1) {
		edges = append(edges, m[1])
	}
	for _, b := range backends {
		if !slices.Contains(edges, b) {
			problems = append(problems, "GRPCRoute ведёт в "+b+", связи internalGateway в model.c4 нет")
		}
	}
	for _, e := range edges {
		if !slices.Contains(backends, e) {
			problems = append(problems, "model.c4: internalGateway ведёт в "+e+", GRPCRoute нет")
		}
	}

	values, err := filepath.Glob(filepath.Join(root, "ops/kind/values/*.yaml"))
	if err != nil {
		return nil, err
	}
	for _, path := range values {
		release := strings.TrimSuffix(filepath.Base(path), ".yaml")
		if strings.Contains(release, "grafana-cloud") || strings.HasSuffix(release, "-dev") {
			continue
		}
		title, ok := kindValues[release]
		if !ok {
			title = release
		}
		if !strings.Contains(model, "'"+title+"'") {
			problems = append(problems, fmt.Sprintf("release %s: процесса '%s' нет в model.c4", release, title))
		}
	}
	index, err := os.ReadFile(filepath.Join(root, adrIndexPath))
	if err != nil {
		return nil, err
	}
	adrs, err := listADRs(root)
	if err != nil {
		return nil, err
	}
	for _, adr := range adrs {
		rel, err := filepath.Rel(filepath.Dir(filepath.Join(root, adrIndexPath)), adr)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(string(index), "("+filepath.ToSlash(rel)+")") {
			problems = append(problems, adrIndexPath+": нет ссылки на "+filepath.ToSlash(rel))
		}
	}
	slices.Sort(problems)
	return problems, nil
}

// listADRs — ADR общесистемные и всех boundaries.
func listADRs(root string) ([]string, error) {
	var all []string
	for _, pattern := range []string{"docs/adr/[0-9]*.md", "boundaries/*/docs/adr/[0-9]*.md"} {
		found, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			return nil, err
		}
		all = append(all, found...)
	}
	return all, nil
}

func readRouteBackends(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var backends []string
	dec := yaml.NewDecoder(f)
	for {
		var doc struct {
			Kind string `yaml:"kind"`
			Spec struct {
				Rules []struct {
					BackendRefs []struct {
						Name string `yaml:"name"`
					} `yaml:"backendRefs"`
				} `yaml:"rules"`
			} `yaml:"spec"`
		}
		if err := dec.Decode(&doc); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if doc.Kind != "GRPCRoute" {
			continue
		}
		for _, r := range doc.Spec.Rules {
			for _, b := range r.BackendRefs {
				backends = append(backends, b.Name)
			}
		}
	}
	slices.Sort(backends)
	return slices.Compact(backends), nil
}
