# Общесистемные цели. Команды application boundaries находятся в их собственных Makefile/package.json.
.DEFAULT_GOAL := help

## Тестовое окружение kind (KIND_CLUSTER_NAME, по умолчанию nomnom-test)
include ops/kind/versions.env
export GATEWAY_API_VERSION
export KIND_CLUSTER_NAME ?= nomnom-test
# Host-порт extraPortMappings ops/kind/cluster.yaml: NodePort 30085 (public gateway).
export WEBHOOKS_HOST_PORT ?= 18085
# Локальный порт port-forward Redpanda Console (make kind-console).
KAFKA_CONSOLE_PORT ?= 18086
KIND_SCRIPTS := ops/kind/scripts
# Skaffold запускается из каталога конфигурации: его пути относительны рабочему каталогу.
SKAFFOLD := cd ops/kind/skaffold && skaffold
SKAFFOLD_FLAGS := --kube-context kind-$(KIND_CLUSTER_NAME)
# GRAFANA_CLOUD=1: kind-deploy/kind-dev с profile grafana-cloud (ops/grafana-cloud/README.md).
# KIND_STAND=core: без fixtures проверок (profile core); по умолчанию kind-dev — core,
# kind-deploy — full (нужен make kind-test).
skaffold_app_flags = --module apps $(SKAFFOLD_FLAGS) $(if $(filter core,$(or $(KIND_STAND),$(1))),-p core) $(if $(GRAFANA_CLOUD),-p grafana-cloud)

.PHONY: buf-check buf-breaking buf-generate kind-cluster kind-up kind-local-ca kind-deploy kind-dev kind-test kind-policies kind-policies-delete kind-diagnostics kind-down kind-e2e kind-grafana-cloud-stop kind-console kind-console-down kind-stripe-listen
buf-check: buf-breaking ## проверить формат, lint, компилируемость и совместимость всех Protobuf-модулей
	@buf format --diff --exit-code
	@buf lint
	@buf build
# Baseline — ветка main, пока модули не опубликованы в BSR (ADR 7).
buf-breaking: ## проверить обратную совместимость Protobuf-контрактов с main (правило FILE)
	@buf breaking --against '.git#branch=main'
buf-generate: ## сгенерировать stubs владельцев для всех Protobuf-модулей
	@cd boundaries/orders/infrastructure/grpc && buf generate --template buf.gen.yaml
	@cd boundaries/restaurants/infrastructure/grpc && buf generate --template buf.gen.yaml
	@cd boundaries/delivery/infrastructure/grpc && buf generate --template buf.gen.yaml
	@cd boundaries/payments/infrastructure/grpc && buf generate --template buf.gen.yaml
kind-cluster: ## создать single-node кластер kind из ops/kind/cluster.yaml (существующий переиспользуется)
	@test "$$(kind version | cut -d' ' -f2)" = "$(KIND_VERSION)" || { echo "kind $(KIND_VERSION) is required by ops/kind/versions.env" >&2; exit 1; }
	@test "$$(skaffold version)" = "$(SKAFFOLD_VERSION)" || { echo "skaffold $(SKAFFOLD_VERSION) is required by ops/kind/versions.env" >&2; exit 1; }
	@kind get clusters | grep -Fxq '$(KIND_CLUSTER_NAME)' || \
	  sed -e 's|__WEBHOOKS_HOST_PORT__|$(WEBHOOKS_HOST_PORT)|' ops/kind/cluster.yaml | \
	  kind create cluster --name '$(KIND_CLUSTER_NAME)' --image '$(KIND_NODE_IMAGE)' --config=-
kind-up: kind-cluster ## создать кластер и установить платформу: Cilium (Hubble observer), Envoy Gateway, cert-manager, Temporal Worker Controller, Strimzi
	@$(SKAFFOLD) run --module platform $(SKAFFOLD_FLAGS)
kind-local-ca: ## сохранить корень локального CA в ops/kind/.artifacts/nomnom-local-ca.crt и вывести путь
	@mkdir -p ops/kind/.artifacts
	@kubectl --context kind-$(KIND_CLUSTER_NAME) -n cert-manager get secret nomnom-local-ca \
	  -o jsonpath='{.data.ca\.crt}' | base64 -d >ops/kind/.artifacts/nomnom-local-ca.crt
	@echo ops/kind/.artifacts/nomnom-local-ca.crt
kind-deploy: ## Skaffold: собрать образы, загрузить в kind, развернуть gateway и приложения, дождаться readiness
	@$(SKAFFOLD) run $(call skaffold_app_flags,full) --status-check=true
kind-dev: ## Skaffold dev loop: пересборка и передеплой приложений при изменении исходников
	@$(SKAFFOLD) dev $(call skaffold_app_flags,core)
kind-test: ## проверки окружения: тесты Chainsaw (ops/kind/e2e); NetworkPolicy применяются внутри
	@mkdir -p ops/kind/.artifacts
	@cd ops/kind/e2e && go run github.com/kyverno/chainsaw@$(CHAINSAW_VERSION) test \
	  --kube-context kind-$(KIND_CLUSTER_NAME) --config .chainsaw.yaml --test-dir .
kind-policies: ## применить NetworkPolicy (ops/kind/manifests/netpol, kustomization)
	@kubectl --context kind-$(KIND_CLUSTER_NAME) apply -k ops/kind/manifests/netpol
kind-policies-delete: ## удалить NetworkPolicy
	@kubectl --context kind-$(KIND_CLUSTER_NAME) delete -k ops/kind/manifests/netpol --ignore-not-found
kind-diagnostics: ## сохранить logs, events, Gateway API и Cilium/Hubble в ops/kind/.artifacts
	@$(KIND_SCRIPTS)/diagnostics.sh
kind-down: ## удалить тестовый кластер (несуществующий — не ошибка)
	@kind delete cluster --name '$(KIND_CLUSTER_NAME)'
kind-grafana-cloud-stop: ## остановить Alloy → Grafana Cloud, чтобы не расходовать квоту free tier
	@kubectl --context kind-$(KIND_CLUSTER_NAME) -n nomnom-observability scale deployment/alloy --replicas=0
kind-console: ## Redpanda Console (Kafka UI) на http://localhost:$(KAFKA_CONSOLE_PORT); по умолчанию не ставится
	@kubectl --context kind-$(KIND_CLUSTER_NAME) apply -f ops/kind/manifests/messaging/console.yaml
	@kubectl --context kind-$(KIND_CLUSTER_NAME) -n nomnom-infra rollout status deployment/console --timeout=180s
	@echo "Redpanda Console: http://localhost:$(KAFKA_CONSOLE_PORT) (Ctrl+C останавливает port-forward, pod остаётся)"
	@kubectl --context kind-$(KIND_CLUSTER_NAME) -n nomnom-infra port-forward svc/console $(KAFKA_CONSOLE_PORT):8080
kind-console-down: ## удалить Redpanda Console
	@kubectl --context kind-$(KIND_CLUSTER_NAME) delete -f ops/kind/manifests/messaging/console.yaml --ignore-not-found
kind-stripe-listen: ## доставлять webhooks Stripe test mode в payments; ключ — из Secret payments-stripe (boundaries/payments/README.md)
	@key="$$(kubectl --context kind-$(KIND_CLUSTER_NAME) -n nomnom-payments get secret payments-stripe -o jsonpath='{.data.api-key}' | base64 -d)"; \
	test -n "$$key" || { echo "secret payments-stripe with api-key is required" >&2; exit 1; }; \
	STRIPE_API_KEY="$$key" exec stripe listen --all-snapshot --skip-verify \
	  --forward-to https://webhooks.nomnom.localhost:$(WEBHOOKS_HOST_PORT)/webhooks/provider
kind-e2e: ## полный неинтерактивный прогон для CI; KIND_KEEP=1 сохраняет кластер
	@$(KIND_SCRIPTS)/e2e.sh

## Black-box проверка runtime contract (docs/cloud-native-runtime.md); RUNTIME_SERVICES сужает набор
RUNTIME_SERVICES ?=
.PHONY: runtime-contract
runtime-contract: ## собрать backend-приложения и проверить health, SIGTERM/SIGINT, drain и exit code
	@python3 ops/runtime-contract/contract_test.py $(RUNTIME_SERVICES)

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
		$(GCX) dev lint run boundaries/*/ops/grafana/dashboards/overview.json ops/grafana/dashboards/platform.json $$preview/*.json
grafana-serve: ## gcx dev serve: локальный preview dashboards (нужен gcx context с Grafana для запросов)
	@$(GCX) dev serve boundaries/*/ops/grafana/dashboards
grafana-cloud-push: ## загрузить overview dashboards в stack Grafana Cloud (ops/grafana-cloud/README.md)
	@GCX=$(GCX) ops/grafana-cloud/push-dashboards.sh
grafana-tools: ## установить gcx $(GCX_VERSION) в GOBIN
	go install github.com/grafana/gcx/cmd/gcx@$(GCX_VERSION)

## Semantic conventions метрик (docs/observability.md#именование-метрик): registry ops/semconv, OpenTelemetry Weaver
WEAVER_VERSION ?= v0.28.0
WEAVER ?= weaver
# Правила naming_conventions и stability — те же, что у open-telemetry/semantic-conventions;
# пакет не выпускает тегов, поэтому закреплён commit.
WEAVER_PACKAGES ?= https://github.com/open-telemetry/opentelemetry-weaver-packages.git@b524723bedec1cc8d9ef83c924a231348a669ebe
.PHONY: semconv-check semconv-tools
semconv-check: ## проверить имена, units и attributes метрик registry ops/semconv (Weaver + policies)
	@$(WEAVER) registry check --v2 -r ops/semconv -p ops/semconv/policies \
		--policy="$(WEAVER_PACKAGES)[policies/check/naming_conventions]" \
		--policy="$(WEAVER_PACKAGES)[policies/check/stability]"
semconv-tools: ## установить weaver $(WEAVER_VERSION) в GOBIN (brew weaver — другой инструмент)
	@arch=$$(uname -m | sed 's/arm64/aarch64/'); os=$$(uname -s | sed 's/Darwin/apple-darwin/; s/Linux/unknown-linux-gnu/'); \
		url=https://github.com/open-telemetry/weaver/releases/download/$(WEAVER_VERSION)/weaver-$$arch-$$os.tar.xz; \
		dir=$$(mktemp -d) && curl -sSLf "$$url" -o "$$dir/w.tar.xz" && curl -sSLf "$$url.sha256" -o "$$dir/w.sha256" && \
		(cd "$$dir" && [ "$$(shasum -a 256 w.tar.xz | cut -d' ' -f1)" = "$$(cut -d' ' -f1 w.sha256)" ]) && \
		tar -xf "$$dir/w.tar.xz" -C "$$dir" && install -m 0755 "$$dir"/weaver-*/weaver "$$(go env GOPATH)/bin/weaver"

## Архитектура сервисов (docs/architecture, ADR 44)
LIKEC4_VERSION ?= 1.60.0
LIKEC4 := npx --yes likec4@$(LIKEC4_VERSION)

.PHONY: arch-dev arch-validate arch-generate arch-check
arch-dev: ## LikeC4: схемы архитектуры в браузере с обновлением при правке
	@$(LIKEC4) start docs/architecture
arch-validate: ## LikeC4: проверить модель docs/architecture
	@$(LIKEC4) validate docs/architecture
arch-generate: ## сгенерировать каталог событий docs/events-catalog.md
	@cd ops/architecture && go run . -root ../..
arch-check: arch-validate ## проверить модель, сверить её с ops, каталог событий без диффа, индекс ADR и теги @AC/@FR в .feature
	@cd ops/architecture && go test ./... && go run . -root ../.. -check

.PHONY: help
help: ## показать цели
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'
