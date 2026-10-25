# Tasks

- [x] 1.1 Закрепить версию и SHA-256 Cosmo Router; `boundaries/bff/Dockerfile` и production config; проверка подмены checksum (`FR-502`, `AC-574`).
- [x] 1.2 `ops/kind/scripts/bff-identity.sh`, вызов из skaffold; release и artifact BFF; values BFF и контекстов; NetworkPolicy BFF.
- [x] 1.3 Сквозная проверка в `ops/kind` (`FR-503`, `AC-575`, `AC-576`) и прогон на общем кластере `nomnom-test`; отметить 4.1 и 4.3 в `add-user-identity-assertions`.
- [x] 1.4 README BFF (контейнеризация, развёртывание), `ops/kind/README.md`, ADR 10; `openspec validate deploy-bff-in-kind --strict`.
