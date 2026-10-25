# Customer UI operations

Этот каталог принадлежит deployable customer UI application boundary. Здесь будут находиться её container image, Helm chart, hosting/runtime configuration, CSP и правила публикации immutable assets.

Согласно [ADR 12](../../../docs/adr/0012-helm-charts-per-deployable-application.md), операционные файлы приложения размещаются в его собственном `ops/`. Сейчас Dockerfile и Helm manifests намеренно не создаются: ещё не приняты hosting model, SPA/SSR решение, runtime image contract, public configuration delivery и cache strategy.

После принятия этих решений artifacts добавляются вместе с исполняемым deployment contract и проверками, а не как пустые заготовки.
