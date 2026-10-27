# 34. Declarative kind bootstrap

Date: 2026-10-25

## Status

Accepted

Уточняет [14. Использовать kind для тестового окружения Kubernetes](0014-использовать-kind-для-тестового-окружения-kubernetes.md)

## Context

Bootstrap kind-окружения делали shell-скрипты в `ops/kind/scripts`, вызываемые hook'ами
Skaffold и Make: `cluster.sh` (проверка версий, `kind create`, предзагрузка образов
платформы через `docker save | ctr import`), `local-ca.sh`, `authz.sh`, `postgres.sh`,
`mongodb.sh`, `identity-jwks.sh`, `bff-identity.sh`, `grafana-cloud.sh`. Они генерировали
секреты на хосте (`openssl rand`, `openssl genpkey`), читали существующие значения из
кластера, чтобы не ротировать их, собирали JWKS через `openssl` и встроенный Python,
публиковали Secret и ConfigMap через `kubectl create --dry-run | kubectl apply` и
`kubectl exec` в MongoDB. Логика расползалась по скриптам, зависела от инструментов хоста
(LibreSSL против OpenSSL 3, Docker credential helper) и не описывала желаемое состояние.

Потребители результатов: values окружения (`ops/kind/values`) и charts контекстов ссылаются
на Secret `spicedb-preshared-key` (`preshared-key`), `<owner>-postgres` (`dsn`),
`restaurants-mongodb` (`uri`), `bff-assertion-key`, ConfigMap `cluster-jwks`, `bff-jwks`,
`idp-jwks` (`jwks.json`), хранилища — Secret `postgres-<owner>` (`password`) и
`mongodb-restaurants` (`root-password`, `password`, `keyfile`), Alloy — `grafana-cloud-otlp`
и `alloy-postgres`.

Ограничение: Skaffold добавляет label `skaffold.dev/run-id` в pod template и для `rawYaml`,
и для своих Helm releases. Хранилища тестового окружения держат данные в памяти и
`emptyDir`, поэтому их нельзя ставить через деплоеры Skaffold: каждый deploy перезапускал бы
их после миграций приложений.

## Decision

Bootstrap описывается chart `ops/kind/bootstrap` (`kind-bootstrap`, release `bootstrap` в
`nomnom-data`). Before-hook helm-деплоера Skaffold применяет `manifests/base/namespaces.yaml`
и выполняет одну команду `helm upgrade --install … --take-ownership --server-side=true
--force-conflicts --wait`; файлы вне chart (`authorization/schema.zed`,
`authorization/bootstrap.yaml`, `tests/identity/fixtures/idp-jwks.json`) передаются
`--set-file`. Helm ставит release сам, без post-renderer Skaffold, поэтому pod templates
меняются только вместе с chart.

| Было | Стало |
| --- | --- |
| `local-ca.sh`; `manifests/certs/issuers.yaml` в `rawYaml` | ClusterIssuer и Certificate `nomnom-local-ca` (cert-manager) в `templates/certs.yaml`; корень для клиентов — `make kind-local-ca` |
| `bff-identity.sh`: `openssl genpkey` → Secret `bff-assertion-key` (`key.pem`) | Certificate `bff-assertion-key` (ECDSA P-256, PKCS#8, `rotationPolicy: Never`, self-signed issuer): cert-manager создаёт Secret с `tls.key`/`tls.crt`; BFF читает `tls.key` |
| `bff-identity.sh` и `identity-jwks.sh`: JWKS на хосте | post-install/post-upgrade Job `identity-jwks` (Python stdlib, `files/publish-jwks.py`): `/openid/v1/jwks` и проверка issuer, публичный ключ BFF из `tls.crt`, server-side apply `cluster-jwks` и `bff-jwks`; RBAC — `get` одного Secret и `get`/`patch` двух ConfigMap |
| `bff-identity.sh`: ConfigMap `idp-jwks` | шаблон ConfigMap из `--set-file` |
| `authz.sh` | ConfigMap `spicedb-bootstrap` из `--set-file`, Secret `spicedb-preshared-key` в `nomnom-authz` и неймспейсах контекстов, Deployment SpiceDB с checksum bootstrap в pod template |
| `postgres.sh`, `mongodb.sh secrets`, `grafana-cloud.sh` (DSN Alloy) | Secrets из `lookup` существующего значения или `randAlphaNum`; DSN, URI и `alloy-postgres` выводятся из того же значения в одном шаблоне |
| `kubectl apply` хранилищ и `kubectl wait` в hook | Deployments и Services хранилищ в chart, `helm --wait` |
| `mongodb.sh init` (`kubectl exec`) | post-install/post-upgrade Job `mongodb-restaurants-init`: `mongosh` по сети, `rs.initiate` и пользователь `restaurants` идемпотентно |
| `grafana-cloud.sh`: Secret из `.env.grafana-cloud`, `kubectl apply` Alloy, `rollout restart` | profile `grafana-cloud` добавляет kustomization `manifests/observability`: `secretGenerator` из того же файла (лишние ключи удаляет patch), суффикс-хеш перезапускает Alloy; пустые значения отвергает init container |
| `cluster.sh` | `make kind-cluster`: проверка версий kind и Skaffold, `kind create cluster --config=-` из `cluster.yaml` с подстановкой host-портов; `make kind-down` — `kind delete cluster` |

Рассмотренные варианты:

- Helm hooks Job для генерации паролей — лишний pod и RBAC на запись Secret, тогда как
  `lookup` + `randAlphaNum` дают тот же результат декларативно;
- External Secrets / Secrets Store CSI — нужен внешний store, которого в kind нет;
- секреты как ресурсы Skaffold (`rawYaml`) — Skaffold не генерирует значения;
- реплицировать ConfigMap через reflector или trust-manager — trust-manager распространяет
  только PEM-бандлы, а reflector — ещё один controller ради двух ConfigMap;
- Job в `nomnom-bff` с монтированием только `tls.crt` — на неймспейс действуют
  NetworkPolicy контекстов, и Job не достал бы API server без отдельной
  CiliumNetworkPolicy. Job работает в `nomnom-data` и читает Secret через API (`get`
  одного имени); `tls.key` он не использует;
- образ `registry.k8s.io/kubectl` — distroless без shell, не выполняет `get --raw` в файл и
  не преобразует ключ; официальный `python` alpine закрывает и API, и разбор X.509;
- ctlptl для декларативного кластера — не CNCF, ещё один бинарь ради одной команды
  `kind create cluster --config`;
- предзагрузка образов платформы в узел (`docker save | ctr import`) не переносится: это
  оптимизация, завязанная на Docker хоста (и его credential helper). Узел скачивает образы
  сам; если время создания кластера станет проблемой, декларативный путь — pull-through
  registry-кеш (`registry:3` с `REGISTRY_PROXY_REMOTEURL`) и `containerdConfigPatches` kind.

## Consequences

Состояние bootstrap описано манифестами одного chart; host-зависимости — `helm`, `kubectl`,
`kind`. Учётные данные генерируются в кластере и не меняются между deploy: `lookup` читает
существующий Secret, в том числе созданный прежними скриптами. `helm template` и
`--dry-run=client` без кластера рендерят новые случайные значения — это ожидаемо.

Release `bootstrap` принимает ресурсы, созданные скриптами (`--take-ownership`); образы
хранилищ теперь закреплены digest, поэтому первый deploy после перехода один раз
перезапускает SpiceDB, PostgreSQL и MongoDB (данные `emptyDir` теряются, миграции
приложений процессы выполняют заново при старте, ADR 42).

Ключ BFF принадлежит cert-manager: Secret `bff-assertion-key` содержит `tls.key` вместо
`key.pem`. Потребители ключа — BFF (`values/bff.yaml`) и проверка 7 `verify.sh` (подпись
user assertion ключом BFF) — читают `tls.key`. Старый Secret со скриптовым `key.pem` нужно удалить один раз: cert-manager
выпустит новый ключ, и Job опубликует его JWKS.

ConfigMap `cluster-jwks` и `bff-jwks` принадлежат field manager `kind-bootstrap`, а не
release, и после `helm uninstall` остаются (кластер kind удаляется целиком). Каждый
`skaffold dev`-цикл выполняет `helm upgrade` и пересоздаёт hook Jobs, как раньше скрипты.
