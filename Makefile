# Общесистемные цели. Команды application boundaries находятся в их собственных Makefile/package.json.
.DEFAULT_GOAL := help

## Тестовое окружение kind (KIND_CLUSTER_NAME, по умолчанию nomnom-test)
include ops/kind/versions.env
export GATEWAY_API_VERSION
export KIND_CLUSTER_NAME ?= nomnom-test
KIND_SCRIPTS := ops/kind/scripts
# Skaffold запускается из каталога конфигурации: его пути относительны рабочему каталогу.
SKAFFOLD := cd ops/kind/skaffold && skaffold
SKAFFOLD_FLAGS := --kube-context kind-$(KIND_CLUSTER_NAME)
# GRAFANA_CLOUD=1: kind-deploy/kind-dev с profile grafana-cloud (ops/grafana-cloud/README.md).
SKAFFOLD_APP_FLAGS := --module apps $(SKAFFOLD_FLAGS) $(if $(GRAFANA_CLOUD),-p grafana-cloud)

.PHONY: buf-check buf-generate kind-up kind-deploy kind-dev kind-test kind-policies kind-policies-delete kind-diagnostics kind-down kind-e2e kind-grafana-cloud-stop
buf-check: ## проверить формат, lint и компилируемость всех Protobuf-модулей
	@buf format --diff --exit-code
	@buf lint
	@buf build
buf-generate: ## сгенерировать stubs владельцев для всех Protobuf-модулей
	@cd boundaries/orders/infrastructure/grpc && buf generate --template buf.gen.yaml
	@cd boundaries/restaurants/infrastructure/grpc && buf generate --template buf.gen.yaml
	@cd boundaries/delivery/infrastructure/grpc && buf generate --template buf.gen.yaml
	@cd boundaries/payments/infrastructure/grpc && buf generate --template buf.gen.yaml
kind-up: ## создать кластер, предзагрузить образы платформы, установить Cilium и Envoy Gateway
	@$(KIND_SCRIPTS)/cluster.sh create
	@$(SKAFFOLD) run --module platform $(SKAFFOLD_FLAGS)
kind-deploy: ## Skaffold: собрать образы, загрузить в kind, развернуть gateway и приложения, дождаться readiness
	@$(SKAFFOLD) run $(SKAFFOLD_APP_FLAGS) --status-check=true
kind-dev: ## Skaffold dev loop: пересборка и передеплой приложений при изменении исходников
	@$(SKAFFOLD) dev $(SKAFFOLD_APP_FLAGS)
kind-test: ## проверки gateway и NetworkPolicy (фаза A без политик, фаза B с ними)
	@$(KIND_SCRIPTS)/verify.sh
kind-policies: ## применить NetworkPolicy
	@$(KIND_SCRIPTS)/policies.sh apply
kind-policies-delete: ## удалить NetworkPolicy
	@$(KIND_SCRIPTS)/policies.sh delete
kind-diagnostics: ## сохранить logs, events, Gateway API и Cilium/Hubble в ops/kind/.artifacts
	@$(KIND_SCRIPTS)/diagnostics.sh
kind-down: ## удалить тестовый кластер
	@$(KIND_SCRIPTS)/cluster.sh delete
kind-grafana-cloud-stop: ## остановить Alloy → Grafana Cloud, чтобы не расходовать квоту free tier
	@kubectl --context kind-$(KIND_CLUSTER_NAME) -n nomnom-observability scale deployment/alloy --replicas=0
kind-e2e: ## полный неинтерактивный прогон для CI; KIND_KEEP=1 сохраняет кластер
	@$(KIND_SCRIPTS)/e2e.sh

## Black-box проверка runtime contract (docs/cloud-native-runtime.md); RUNTIME_SERVICES сужает набор
RUNTIME_SERVICES ?=
.PHONY: runtime-contract
runtime-contract: ## собрать backend-приложения и проверить health, SIGTERM/SIGINT, drain и exit code
	@python3 ops/runtime-contract/contract_test.py $(RUNTIME_SERVICES)

## Размер исходных файлов (openspec spec source-file-size); FILE_SIZE_PATHS сужает проверку
FILE_SIZE_PATHS ?= boundaries
.PHONY: check-file-size
check-file-size: ## production-файлы не больше 300 строк, тесты не больше 500; исключения — ops/checks/file-size-allowlist.txt
	@ops/checks/file-size.sh $(FILE_SIZE_PATHS)

## Authorization (ADR 18): schema и bootstrap в authorization/
SPICEDB_IMAGE ?= authzed/spicedb:v1.57.0
ZED_IMAGE ?= authzed/zed:v1.2.1
SPICEDB_CONTAINER ?= nomnom-spicedb
.PHONY: authz-validate spicedb-dev spicedb-dev-stop
authz-validate: ## zed validate: grants развёртывания (bootstrap.yaml) и пути пользовательских прав (validation.yaml)
	docker run --rm -v "$(CURDIR)/authorization":/authz -w /authz $(ZED_IMAGE) validate bootstrap.yaml validation.yaml

spicedb-dev: ## SpiceDB (memory) с bootstrap на localhost:50051, preshared key devkey
	docker run -d --rm --name $(SPICEDB_CONTAINER) -p 50051:50051 -v "$(CURDIR)/authorization":/authz -w /authz \
		$(SPICEDB_IMAGE) serve --grpc-preshared-key devkey --datastore-engine memory --datastore-bootstrap-files bootstrap.yaml

spicedb-dev-stop: ## остановить локальный SpiceDB
	-docker rm -f $(SPICEDB_CONTAINER)

## Grafana dashboards as code (ADR 29): генератор ops/grafana, артефакты boundaries/<service>/ops/grafana
GCX_VERSION ?= v1.7.1
GCX ?= gcx
.PHONY: grafana-generate grafana-check grafana-serve grafana-tools grafana-cloud-push
grafana-generate: ## сгенерировать overview dashboards, их README и docs/observability/metric-inventory.md
	@cd ops/grafana && go run ./cmd/dashboards -root ../..
grafana-check: ## CI: тесты генератора, регенерация без диффа, gcx lint закоммиченного и live-варианта
	@cd ops/grafana && go test ./... && go run ./cmd/dashboards -root ../.. -check
	@preview=$$(mktemp -d) && (cd ops/grafana && go run ./cmd/dashboards -preview $$preview) && \
		$(GCX) dev lint run boundaries/*/ops/grafana/dashboards/overview.json $$preview/*.json
grafana-serve: ## gcx dev serve: локальный preview dashboards (нужен gcx context с Grafana для запросов)
	@$(GCX) dev serve boundaries/*/ops/grafana/dashboards
grafana-cloud-push: ## загрузить overview dashboards в stack Grafana Cloud (ops/grafana-cloud/README.md)
	@GCX=$(GCX) ops/grafana-cloud/push-dashboards.sh
grafana-tools: ## установить gcx $(GCX_VERSION) в GOBIN
	go install github.com/grafana/gcx/cmd/gcx@$(GCX_VERSION)

.PHONY: help
help: ## показать цели
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'
