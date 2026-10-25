# 30. CI is Described but Runs Only on Demand

Date: 2026-10-25

## Status

Accepted

Связан с [10. Использовать multi-stage container builds](0010-использовать-multi-stage-container-builds.md), [14. Использовать kind для тестового окружения Kubernetes](0014-использовать-kind-для-тестового-окружения-kubernetes.md), [19. BDD for Business Acceptance Testing](0019-bdd-for-business-acceptance-testing.md), [29. Grafana Dashboards as Code](0029-grafana-dashboards-as-code.md)

## Context

Каждая application boundary уже предоставляет единообразную команду проверки: `make check` в `orders`, `delivery` и `payments`, `npm run check` в `restaurants`, `ui` и BFF ([команды разработки](../../boundaries/README.md#команды-разработки)). Integration-тесты запускаются отдельными целями (`make test-integration`, `make test-postgres`, `make test-integration-kafka`) и поднимают зависимости в контейнерах, E2E — в kind (ADR 14).

Проект ограничен лимитами GitHub Actions. Полная проверка шести boundaries на четырёх языках с контейнерными integration-тестами на каждый push и pull request быстро исчерпывает минуты и блокирует остальные сборки проекта, включая необходимые workflows. Расход минут на авто-проверки не должен останавливать работу.

При этом пайплайн проверки нужен в виде кода: чтобы его можно было запустить вручную перед важным merge, а при снятии лимитов включить без проектирования заново.

## Decision

**Источник истины — локальные проверки.** Перед push автор запускает `make check` / `npm run check` затронутых boundaries, а integration-цели — если изменены adapters, persistence, messaging или composition root. Merge без зелёного локального `check` затронутых boundaries не выполняется.

**Workflow описан, но автоматически не запускается.** [`.github/workflows/ci.yaml`](../../.github/workflows/ci.yaml) содержит по job на boundary, которые вызывают те же команды `check`, что и локальная проверка, и отдельную job integration-тестов `orders`. Единственный trigger — `workflow_dispatch` с выбором boundary; `push` и `pull_request` не подключены. Job не дублирует логику Makefile/`package.json`: workflow только готовит toolchain и вызывает ту же команду.

**Исключение.** [`grafana-dashboards.yaml`](../../.github/workflows/grafana-dashboards.yaml) (ADR 29) остаётся автоматическим: он ограничен путями `ops/grafana` и dashboards, выполняется за минуты и ловит расхождение закоммиченного артефакта с генератором, которое локально легко пропустить. Новые автоматические workflows добавляются только с таким же узким path filter и отдельным решением.

**Включение.** Чтобы включить CI, в `ci.yaml` добавляются `pull_request` и `push` в `main` с `paths` по boundaries; jobs уже разделены, поэтому path filter запускает только затронутые. Изменение trigger оформляется новым ADR, заменяющим этот.

## Consequences

- Минуты GitHub Actions расходуются только по явному запуску; сборки проекта не блокируются авто-проверками.
- Корректность ветки держится на дисциплине автора: регрессия, пропущенная локально, видна только при ручном запуске workflow или в kind E2E. Компенсация — одинаковые команды `check` во всех boundaries и integration-тесты, которые сами поднимают зависимости через testcontainers.
- Workflow может устареть незаметно, потому что не запускается регулярно. Его запускают вручную при изменении toolchain, команд `check` или перед релизом.
- Включение CI — изменение triggers, а не новая разработка пайплайна.
