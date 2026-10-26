// Архитектурные тесты: направление импортов слоёв ADR 6 проверяется по графу
// пакетов `go list`, поэтому возвращённая зависимость роняет `go test ./...`.
// Проверяются прямые импорты production-кода; test-support пакеты
// (`*test`: ordertest, repositorytest) и `_test.go` файлы не проверяются.
package orders_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const module = "nomnom/orders"

type goPackage struct {
	ImportPath string
	Imports    []string
}

// layer — слой пакета по его пути относительно модуля.
type layer string

const (
	domains        layer = "domains"
	applications   layer = "applications"
	infrastructure layer = "infrastructure"
	shared         layer = "pkg"
	compositionDI  layer = "di"
	command        layer = "cmd"
)

func layerOf(importPath string) (layer, bool) {
	rel, ok := strings.CutPrefix(importPath, module+"/")
	if !ok {
		return "", false
	}
	parts := strings.Split(rel, "/")
	if parts[0] == "slices" && len(parts) >= 3 {
		parts = parts[2:]
	}
	switch l := layer(parts[0]); l {
	case domains, applications, infrastructure, shared, compositionDI, command:
		return l, true
	}
	return "", false
}

// isStandard: путь модуля `nomnom/orders` тоже без точки в первом элементе.
func isStandard(importPath string) bool {
	return !strings.HasPrefix(importPath, module+"/") &&
		!strings.Contains(strings.Split(importPath, "/")[0], ".")
}

func isTestSupport(importPath string) bool {
	parts := strings.Split(importPath, "/")
	return strings.HasSuffix(parts[len(parts)-1], "test")
}

func productionPackages(t *testing.T) []goPackage {
	t.Helper()
	cmd := exec.Command("go", "list", "-json=ImportPath,Imports", "./...")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("go list: %v\n%s", err, exitErr.Stderr)
		}
		t.Fatalf("go list: %v", err)
	}
	var packages []goPackage
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var pkg goPackage
		if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode go list: %v", err)
		}
		if !isTestSupport(pkg.ImportPath) {
			packages = append(packages, pkg)
		}
	}
	if len(packages) == 0 {
		t.Fatal("go list returned no packages")
	}
	return packages
}

// violations — импорты пакетов слоя from, которые отвергает allowed.
func violations(t *testing.T, from layer, allowed func(pkg, imported string) bool) []string {
	t.Helper()
	var found []string
	for _, pkg := range productionPackages(t) {
		if l, ok := layerOf(pkg.ImportPath); !ok || l != from {
			continue
		}
		for _, imported := range pkg.Imports {
			if !allowed(pkg.ImportPath, imported) {
				found = append(found, pkg.ImportPath+" -> "+imported)
			}
		}
	}
	return found
}

func inLayer(importPath string, layers ...layer) bool {
	l, ok := layerOf(importPath)
	return ok && slices.Contains(layers, l)
}

// Доменная модель зависит только от stdlib, decimal и доменов bounded context:
// без Protobuf, Kafka, Temporal, драйверов БД, DI и Application/Transport DTO.
func TestDomainsDependOnlyOnDomainsAndStdlib(t *testing.T) {
	found := violations(t, domains, func(_, imported string) bool {
		return isStandard(imported) ||
			imported == "github.com/shopspring/decimal" ||
			inLayer(imported, domains)
	})
	if len(found) > 0 {
		t.Errorf("domains import outside the domain model:\n%s", strings.Join(found, "\n"))
	}
}

// Value objects не зависят от агрегатов и сущностей своего домена.
func TestValueObjectsDoNotImportTheirAggregate(t *testing.T) {
	found := violations(t, domains, func(pkg, imported string) bool {
		root, isVO := strings.CutSuffix(pkg, "/vo")
		return !isVO || imported != root
	})
	if len(found) > 0 {
		t.Errorf("value objects import their aggregate:\n%s", strings.Join(found, "\n"))
	}
}

// Use cases зависят от доменов, других use cases и прикладных shared packages;
// adapters, transport, generated code и composition root им недоступны.
func TestApplicationsDoNotDependOnAdaptersOrFrameworks(t *testing.T) {
	found := violations(t, applications, func(_, imported string) bool {
		return isStandard(imported) ||
			inLayer(imported, domains, applications) ||
			imported == module+"/pkg/application" ||
			imported == module+"/pkg/telemetry"
	})
	if len(found) > 0 {
		t.Errorf("applications import adapters or frameworks:\n%s", strings.Join(found, "\n"))
	}
}

// Shared packages не знают о слайсах, adapters и composition root.
func TestSharedPackagesDoNotImportSlicesOrProcesses(t *testing.T) {
	found := violations(t, shared, func(_, imported string) bool {
		return !strings.HasPrefix(imported, module+"/slices/") &&
			!inLayer(imported, infrastructure, compositionDI, command)
	})
	if len(found) > 0 {
		t.Errorf("pkg imports slices or processes:\n%s", strings.Join(found, "\n"))
	}
}

// Adapters реализуют контракты слайсов и не тянут composition root.
func TestInfrastructureDoesNotImportCompositionRoot(t *testing.T) {
	found := violations(t, infrastructure, func(_, imported string) bool {
		return !inLayer(imported, compositionDI, command)
	})
	if len(found) > 0 {
		t.Errorf("infrastructure imports di or cmd:\n%s", strings.Join(found, "\n"))
	}
}

// Сгенерированный Buf-код импортируется только adapters, composition root и
// пакетом-владельцем (ADR 7).
func TestGeneratedCodeStaysInAdapters(t *testing.T) {
	var found []string
	for _, pkg := range productionPackages(t) {
		for _, imported := range pkg.Imports {
			owner, _, generated := strings.Cut(imported, "/gen/")
			if !generated || !strings.HasPrefix(imported, module+"/") {
				continue
			}
			if inLayer(pkg.ImportPath, infrastructure, compositionDI, command) ||
				pkg.ImportPath == owner || strings.HasPrefix(pkg.ImportPath, owner+"/") {
				continue
			}
			found = append(found, pkg.ImportPath+" -> "+imported)
		}
	}
	if len(found) > 0 {
		t.Errorf("generated code imported outside adapters:\n%s", strings.Join(found, "\n"))
	}
}
