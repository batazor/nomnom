# 35. Вход в браузере через Dex и OIDC authorization code + PKCE

Date: 2026-10-25

## Status

Accepted

Дополняет [32. User identity через user assertions, подписанные BFF](0032-user-identity-via-bff-signed-assertions.md)

## Context

ADR 32 оставил открытыми выбор identity provider и способ, которым браузер получает access
token. Без этого UI не может вызвать `placeOrder`, `openRestaurant` и `closeRestaurant` от имени
пользователя, а в kind access tokens подписывает проверка ключом
`tests/identity/fixtures/idp-signing-key.pem` (`ops/kind/scripts/mint-jwt.py`).

Требования проекта к входу минимальны: несколько тестовых пользователей (покупатель, владелец
ресторана, сотрудник кухни, курьер), стабильный `sub`, совпадающий с subject в SpiceDB, JWKS для
Cosmo Router и BFF. Регистрация, восстановление пароля, MFA и federation не нужны. Полноценные
провайдеры (Keycloak, Zitadel) требуют собственной базы, памяти и настройки realm, что для
учебного kind-окружения избыточно.

## Decision

- **Identity provider — [Dex](https://dexidp.io)** со `staticPasswords` и storage `memory`.
  Один stateless Deployment без базы; пользователи и их `userID` (становится `sub`) задаются в
  конфигурации. Issuer, JWKS (`/keys`) и discovery — стандартные OIDC.
- **Браузер получает access token через OIDC authorization code + PKCE** (public client, без
  client secret) и передаёт его в `Authorization: Bearer`. Cosmo Router и BFF проверяют token по
  JWKS Dex, как описано в ADR 32; контексты от Dex не зависят.
- Token хранится в памяти страницы, refresh — повторным входом (короткоживущие сессии
  достаточны для теста). httpOnly cookie и session в BFF не вводятся.
- `mint-jwt.py` и фикстура ключа остаются для e2e-проверок, которым браузер не нужен.

## Consequences

- Вход в kind поднимается одним небольшим Deployment; смена провайдера затрагивает только
  конфигурацию Router, BFF и UI.
- `userID` статических пользователей Dex должны совпадать с subjects в
  `ops/kind/authz/bootstrap-kind.yaml`.
- Хранение пользователей в конфигурации и storage `memory` не подходят для production: при
  выходе за пределы тестового окружения провайдер выбирается заново, без изменений контекстов.
- Token в памяти теряется при перезагрузке страницы — пользователь входит снова.
