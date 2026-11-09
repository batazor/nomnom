# Ops: controlplane

Операционные файлы control plane ([ADR 41](../../../docs/adr/0041-cell-based-architecture-with-control-plane.md)):

```text
ops/
  helm/        # Helm chart приложения (ADR 12)
```

Chart создаёт:

- CRD `Cell` и `Market` (`templates/crds/`, генерирует `make generate`) и `ValidatingAdmissionPolicy` запрета их удаления (`keep`: uninstall их не удаляет);
- Deployment с одной репликой без surge (`maxSurge: 0`) и ServiceAccount;
- ConfigMap с файлом конфигурации процесса `config.yaml` (`apiVersion: config.nomnom.dev/v1alpha1`, `kind: ControlPlane`,
  `metadata.name` — обязательное значение `role`, `spec` — `config.spec` как есть). Файл смонтирован в
  `/etc/nomnom/config.yaml`, путь задаёт env `CONFIG_FILE`, `POD_NAME` — Downward API, аннотация `checksum/config`
  перезапускает pod при изменении файла. Chart дописывает только `service.version` из `image.tag` в
  `spec.telemetry.resourceAttributes`; этот ключ в values — ошибка `helm template`;
- `ClusterRole` чтения реестра и записи его `status` для ServiceAccount и `ClusterRole` `nomnom-topology-editor` для тех, кому кластер разрешит менять реестр;
- Role с RoleBinding в неймспейсе маршрутов публичного gateway (`config.spec.routes.namespace`).

`config.spec.routes.*` и `image.tag` обязательны: без них chart не рендерится. Chart сверяет
`terminationGracePeriodSeconds` с `config.spec.shutdown.readinessDelay + drainTimeout` и берёт порт `metrics` из
`config.spec.management.port`. Телеметрия — `config.spec.telemetry`, `tracing`, `metrics`, `logging`; общий профиль
`ops/kind/values/grafana-cloud.yaml` включает экспорт в Alloy. Values kind — `ops/kind/values/controlplane.yaml`.
