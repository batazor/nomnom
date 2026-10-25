# Клиент тестовых pod (manifests/fixtures/net-probe.yaml): вызовы изнутри кластера от имени
# workload-а в неймспейсе приложения, под его NetworkPolicy. Подключается после lib.sh.
#
# Pod указывается как <namespace>/<deployment>: net-probe в неймспейсах приложений и
# outsider, grpc-caller (ServiceAccount nomnom-bff/bff) для вызовов с service identity.

# Projected token grpc-caller (ADR 27); у net-probe его нет, и вызов идёт без identity.
PROBE_TOKEN_FILE=/var/run/secrets/nomnom/identity/nomnom.orders

# probe_grpc <namespace/deployment> <url> [request] [curl args...]
# gRPC-вызов по h2c, как у gRPC-клиента: request — protobuf-сообщение в формате printf
# (по умолчанию пустое), кадр gRPC с длиной собирается здесь. Результат:
# "<curl exit code> <grpc-status> <grpc-message>". grpc-status читается из headers
# (trailers-only ответ, локальный ответ Envoy) или из trailers (ответ grpc-js);
# grpc-message по спецификации gRPC percent-encoded и декодируется.
# Коды curl: 0 — ответ получен, 6 — DNS, 7 — connection refused, 28 — timeout (silent drop).
probe_grpc() {
  local namespace="${1%%/*}" deployment="${1#*/}" url="$2" request="${3:-}" rc=0 response frame
  shift $(($# < 3 ? $# : 3))
  # Кадр gRPC: флаг сжатия и длина сообщения big-endian; проверкам хватает сообщений < 256 байт.
  frame="\\000\\000\\000\\000\\$(printf '%03o' "$(printf "${request}" | wc -c)")${request}"
  response="$(k -n "${namespace}" exec "deploy/${deployment}" -- sh -c "
    set --
    [ -r ${PROBE_TOKEN_FILE} ] && set -- -H \"authorization: Bearer \$(cat ${PROBE_TOKEN_FILE})\"
    printf '${frame}' | curl -sS -o /dev/null -D - --connect-timeout 3 --max-time 5 \
      --http2-prior-knowledge -X POST -H 'content-type: application/grpc' -H 'te: trailers' \
      \"\$@\" $* --data-binary @- '${url}'" 2>/dev/null)" || rc=$?
  response="$(printf '%s' "${response}" | tr -d '\r')"
  printf '%s %s %s\n' "${rc}" \
    "$(printf '%s\n' "${response}" | sed -n 's/^grpc-status: //p')" \
    "$(printf '%b' "$(printf '%s\n' "${response}" | sed -n 's/^grpc-message: //p' |
      sed 's/%/\\x/g')")" | sed 's/ *$//'
}

# probe_tcp <namespace> <host> <port> → "open" или "closed". Silent drop и отказ одинаково
# дают closed за 3 секунды; недоступный net-probe даёт пустой результат, а не closed.
probe_tcp() {
  k -n "$1" exec deploy/net-probe -- sh -c "nc -z -w 3 '$2' '$3' >/dev/null 2>&1 && echo open || echo closed" \
    2>/dev/null || true
}

# probe_dns <namespace> <name> → "yes" или "no".
probe_dns() {
  k -n "$1" exec deploy/net-probe -- nslookup "$2" >/dev/null 2>&1 && echo yes || echo no
}
