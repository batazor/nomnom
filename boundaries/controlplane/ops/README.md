# Ops: controlplane

Операционные файлы control plane ([ADR 41](../../../docs/adr/0041-cell-based-architecture-with-control-plane.md)):

```text
ops/
  helm/        # Helm chart приложения (ADR 12)
```

Chart создаёт Deployment, Service (порт `grpc` 8086), ServiceAccount, ConfigMap несекретного
`env` и Role с RoleBinding в неймспейсе маршрутов публичного gateway (`routes.namespace`: только
`get`/`create`/`patch` HTTPRoute, HTTPRouteFilter и ConfigMap). Секреты (DSN PostgreSQL, preshared
key SpiceDB) — внешние Secret. Identity, SpiceDB, PostgreSQL и `routes.*` обязательны: без них
chart не рендерится. Values kind — `ops/kind/values/controlplane.yaml`.
