GO ?= go
GOLANGCI_LINT ?= golangci-lint

.PHONY: check run generate crd-check test lint format format-check

check: crd-check test lint format-check

# Реестр и маршруты (ROUTES__*, ADR 41) доступны через in-cluster config: процесс стартует только
# в pod кластера. ROUTES__* задаёт окружение или CONFIG_FILE (config.example.yaml).
run:
	$(GO) run ./cmd/controlplane

generate:
	$(GO) tool controller-gen object paths=./infrastructure/kubernetes/api/...
	$(GO) tool controller-gen crd paths=./infrastructure/kubernetes/api/... output:crd:artifacts:config=ops/helm/templates/crds
	$(GO) tool wire ./di
	$(GO) tool mockery

# Манифесты CRD в chart и DeepCopy совпадают с типами.
crd-check:
	$(GO) tool controller-gen object paths=./infrastructure/kubernetes/api/...
	$(GO) tool controller-gen crd paths=./infrastructure/kubernetes/api/... output:crd:artifacts:config=ops/helm/templates/crds
	git diff --exit-code -- infrastructure/kubernetes/api ops/helm/templates/crds

# Все тесты, включая k3s в testcontainers (нужен Docker).
test:
	$(GO) test ./...

lint:
	$(GOLANGCI_LINT) run

format:
	$(GO) fmt ./...

format-check:
	$(GOLANGCI_LINT) fmt --diff
