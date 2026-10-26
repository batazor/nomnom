# Ops: controlplane

Операционные файлы control plane ([ADR 41](../../../docs/adr/0041-cell-based-architecture-with-control-plane.md)):

```text
ops/
  helm/        # Helm chart приложения (ADR 12)
```

Chart создаёт Deployment, Service (порт `grpc` 8086), ServiceAccount и ConfigMap несекретного
`env`. Секреты (DSN PostgreSQL, preshared key SpiceDB) — внешние Secret. Identity, SpiceDB и
PostgreSQL обязательны: без них chart не рендерится. Values kind — `ops/kind/values/controlplane.yaml`.
