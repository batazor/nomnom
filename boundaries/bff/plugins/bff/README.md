# BFF Router Plugin

Go plugin реализует GraphQL-поля клиентского BFF для WunderGraph Cosmo Router.

- Публичный контракт: [`src/schema.graphql`](src/schema.graphql).
- Реализация: [`src/main.go`](src/main.go).
- Generated Router/plugin gRPC-контракт: `generated/`.
- Архитектура и команды: [корневой README BFF](../../README.md).

После изменения GraphQL SDL необходимо выполнить `npm run generate` из каталога `boundaries/bff`, реализовать новые generated RPC и добавить тесты. Generated-файлы вручную не редактируются.
