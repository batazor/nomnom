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
  Один stateless Deployment без базы; пользователи задаются в конфигурации. Issuer, JWKS
  (`/keys`) и discovery — стандартные OIDC. `sub` Dex — не `userID`, а его кодировка вместе с
  connector (`base64url(protobuf{userID, "local"})`, для `kind-owner` —
  `CgpraW5kLW93bmVyEgVsb2NhbA`); значение стабильно и служит subject пользователя.
- Audience access token — `nomnom-bff`: UI (public client `nomnom-ui`) запрашивает scope
  `audience:server:client_id:nomnom-bff`, client `nomnom-bff` доверяет `nomnom-ui`
  (`trustedPeers`).
- Access token Dex имеет форму ID token (без `scope`, с `at_hash`). ID token отличается только
  `nonce`, поэтому UI не передаёт `nonce` (PKCE защищает обмен code), а BFF отклоняет token с
  `nonce`; `scope` не требуется.
- Ключи подписи Dex создаёт при старте и ротирует сам, поэтому Router и BFF читают JWKS по URL
  с обновлением. BFF принимает несколько providers (`BFF__IDP_PROVIDERS`: issuer и JWKS), ключи
  одного issuer не подтверждают token другого.
- **Браузер получает access token через OIDC authorization code + PKCE** (public client, без
  client secret) и передаёт его в `Authorization: Bearer`. Cosmo Router и BFF проверяют token по
  JWKS Dex, как описано в ADR 32; контексты от Dex не зависят.
- Access token хранится только в памяти страницы. UI запрашивает `offline_access`; refresh
  token каждой роли лежит в `sessionStorage` вкладки. Access token обновляется за минуту до
  истечения, Dex ротирует refresh token при каждом обновлении; отказ в обновлении — выход
  этой роли. После перезагрузки вход восстанавливается обменом refresh token, закрытие вкладки
  его завершает. «Выйти» стирает refresh token: endpoints отзыва и logout у Dex нет, поэтому
  `prompt=login` требует пароль при каждом новом входе. httpOnly cookie и session в BFF не
  вводятся.
- `mint-jwt.py` и фикстура ключа остаются для e2e-проверок, которым браузер не нужен: в kind
  BFF и Router доверяют и Dex, и тестовому provider (`test-idp` раздаёт его JWKS по HTTP).

## Consequences

- Вход в kind поднимается одним небольшим Deployment; смена провайдера затрагивает только
  конфигурацию Router, BFF и UI.
- Subjects пользователей Dex в SpiceDB (`ops/kind/authz/bootstrap-kind.yaml`, `ownerId` при
  регистрации ресторана) — закодированный `sub` Dex, а не `userID`. Демо-данные
  (`ops/kind/demo/seed.json`) и dev token issuer используют те же `sub`, поэтому вход через Dex
  и demo-токены действуют от одних пользователей: `customer|owner|kitchen|courier|operator@nomnom.test`,
  пароль `nomnom`.
- Хранение пользователей в конфигурации и storage `memory` не подходят для production: при
  выходе за пределы тестового окружения провайдер выбирается заново, без изменений контекстов.
- Refresh token в `sessionStorage` доступен скриптам страницы: XSS может его украсть. Риск
  ограничен вкладкой, ротацией и CSP; для production выбирается BFF-session с httpOnly cookie.
