# Customer UI: agent instructions

## Edge

Перед customer UI всегда стоит Cloudflare: TLS, CDN-кэш статики и WAF. Origin — nginx со статикой и окружением в `/config.js` (`docs/adr/0002-static-origin-on-nginx-with-runtime-config.md`, `ops/README.md`); решения по кэшу, заголовкам и доставке принимаются с расчётом на Cloudflare:

- Security headers (`Content-Security-Policy`, `Connection-Allowlist`, `frame-ancestors`) отдаёт origin; Cloudflare их не переписывает. Inline-скрипты Cloudflare (Rocket Loader, Email Obfuscation, Web Analytics beacon) выключены, иначе их блокирует CSP.
- Хэшированные assets из `dist/assets/` кэшируются как immutable; `index.html` не кэшируется на edge, чтобы новый релиз виден сразу.
- Ответы, зависящие от заголовков запроса, объявляют `Vary`, который Cloudflare учитывает при кэшировании.
- Реальный IP клиента приходит в `CF-Connecting-IP`, а не в адресе соединения.
