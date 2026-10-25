# 12. Helm Charts per Deployable Application

Date: 2026-10-24

## Status

Accepted

## Context

nomnom состоит из нескольких запускаемых приложений: bounded contexts `orders`,
`restaurants`, `delivery`, `payments` с собственными Dockerfile и BFF на WunderGraph Cosmo
Router. Образы собираются по [ADR 10](0010-использовать-multi-stage-container-builds.md),
но способ описания и развёртывания workload в Kubernetes до сих пор не был определён.
Тестовое окружение проекта основано на kind, поэтому манифесты должны работать в обычном
Kubernetes без cloud-specific ресурсов.

Нужно решить, кто владеет deployment-описанием приложения, где оно хранится, как
соотносятся charts разных приложений и как сочетать Kubernetes `Service` с архитектурным
правилом вызовов через internal gateway.

## Decision

Каждое deployable приложение владеет собственным Helm chart. Операционные файлы приложения
находятся в каталоге `ops/` в корне этого приложения:

```text
boundaries/<service>/ops/helm/
boundaries/bff/ops/helm/
```

Chart создаётся только для каталогов, которые представляют запускаемое приложение.
Chart содержит `Deployment`, `Service`, `ServiceAccount`, `ConfigMap` для несекретной
конфигурации, общие helpers и `NOTES.txt`. Ресурсы помечаются Kubernetes recommended labels
с `app.kubernetes.io/part-of: nomnom`; selector labels ограничены стабильными
`app.kubernetes.io/name` и `app.kubernetes.io/instance`.

Charts независимы друг от друга: каждый устанавливается и обновляется отдельным release и не
зависит от других charts. Umbrella chart сейчас не используется; его появление потребует
отдельного решения.

Базовый chart не содержит значений конкретного окружения. Registry и tag образа, ресурсы,
адреса внешних систем, OpenTelemetry endpoint и kind-specific настройки задаются
environment-specific values-файлами или флагами установки, которые хранятся отдельно от
базового chart. Секреты не попадают в `values.yaml` и `ConfigMap`: они предполагаются
внешними Kubernetes `Secret` и подключаются через `envFrom` или `valueFrom`.

Kubernetes `Service` создаётся для каждого приложения как backend target для internal
gateway и технических механизмов Kubernetes. Архитектурное правило сохраняется: бизнес-вызовы
между сервисами не обращаются к `Service` напрямую и проходят через internal gateway.
Ingress, `HTTPRoute`, `GRPCRoute`, service mesh, базы данных, Kafka, Temporal и observability
collector не входят в chart приложения и определяются отдельными решениями.

Безопасные значения по умолчанию: отдельный `ServiceAccount` без automount token,
`runAsNonRoot`, запрет privilege escalation, сброс всех capabilities, `RuntimeDefault`
seccomp profile. Числовой `runAsUser` задаётся только там, где UID известен из Dockerfile.
Probes поддерживаются chart, но включаются только после появления документированного health
contract приложения. Если приложение состоит из нескольких процессов, например API и Temporal
worker в `orders`, каждый процесс получает отдельный workload, а не второй процесс в том же
контейнере.

kind используется для проверки charts в тестовом окружении: образы задаются через
`image.repository`, `image.tag` и `image.pullPolicy`, что позволяет использовать локально
загруженный образ без registry. Каждый chart проверяется `helm lint` и `helm template`;
установка в кластер выполняется, когда существует runtime-образ приложения.

## Consequences

Deployment-описание развивается вместе с кодом приложения и проходит ревью в том же
изменении. Приложения можно выкатывать и откатывать независимо, а chart остаётся
переносимым между kind и другими Kubernetes-кластерами.

Общие изменения, например новый обязательный label или security default, нужно вносить в
каждый chart отдельно, пока нет library chart или umbrella chart. Состав environment values
и способ их хранения, маршрутизация через gateway, probes, ресурсы и наблюдаемость требуют
последующих решений по мере появления runtime-контрактов приложений. BFF получает chart,
но не может быть установлен до определения его образа согласно ADR 10.
