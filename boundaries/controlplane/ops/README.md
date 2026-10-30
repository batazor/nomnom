# Ops: controlplane

Операционные файлы control plane ([ADR 41](../../../docs/adr/0041-cell-based-architecture-with-control-plane.md)):

```text
ops/
  helm/        # Helm chart приложения (ADR 12)
```

Chart создаёт:

- CRD `Cell` и `Market` (`templates/crds/`, генерирует `make generate`) и `ValidatingAdmissionPolicy` запрета их удаления (`keep`: uninstall их не удаляет);
- Deployment с одной репликой без surge (`maxSurge: 0`), ServiceAccount и ConfigMap несекретного `env`;
- `ClusterRole` чтения реестра и записи его `status` для ServiceAccount и `ClusterRole` `nomnom-topology-editor` для тех, кому кластер разрешит менять реестр;
- Role с RoleBinding в неймспейсе маршрутов публичного gateway (`routes.namespace`).

`routes.*` обязательны: без них chart не рендерится. Values kind — `ops/kind/values/controlplane.yaml`.
