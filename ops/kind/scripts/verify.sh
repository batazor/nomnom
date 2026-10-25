#!/usr/bin/env bash
# Проверки схемы «синхронные вызовы бизнес-API только через internal gateway».
# Фаза A выполняется без NetworkPolicy, фаза B — с ними; по завершении политики остаются.
# Проверки, которым нужны бизнес-RPC, инфраструктура или BFF, выводятся как BLOCKED.
source "$(dirname "$0")/lib.sh"

require_command kubectl jq
assert_test_context

# Прямые вызовы реальных gRPC-методов backend'ов в обход gateway (h2c, бизнес-порт grpc).
RESTAURANTS="http://restaurants.nomnom-restaurants.svc.cluster.local:8082/nomnom.restaurants.v1.RestaurantsService/GetRestaurant"
ORDERS="http://orders.nomnom-orders.svc.cluster.local:8081/nomnom.orders.v1.OrdersService/ListCustomerOrders"
GATEWAY="http://${INTERNAL_GATEWAY_ADDR}"
mkdir -p "${ARTIFACTS_DIR}"
REPORT="${ARTIFACTS_DIR}/verify-report.txt"
: >"${REPORT}"
failures=0

pass() { printf 'PASS     %s\n' "$*" | tee -a "${REPORT}" >&2; }
fail() {
  printf 'FAIL     %s\n' "$*" | tee -a "${REPORT}" >&2
  failures=$((failures + 1))
}
blocked() { printf 'BLOCKED  %s\n' "$*" | tee -a "${REPORT}" >&2; }
section() { printf '\n== %s\n' "$*" | tee -a "${REPORT}" >&2; }

# expect <description> <expected> <actual>
expect() {
  if [[ "$3" == "$2" ]]; then
    pass "$1"
  else
    fail "$1 (expected '$2', got '$3')"
  fi
}

# http <namespace> <url> → "<curl exit code> <HTTP status>": gRPC-вызов с пустым protobuf
# request по h2c, как у gRPC-клиента. HTTP 200 означает, что ответил backend; grpc-status
# приходит в trailers и здесь не проверяется.
# Коды curl: 0 — ответ получен, 6 — DNS, 7 — connection refused, 28 — timeout (silent drop).
http() {
  local namespace="$1" url="$2" status rc=0
  status="$(k -n "${namespace}" exec deploy/net-probe -- sh -c \
    "printf '\\000\\000\\000\\000\\000' | curl -sS -o /dev/null -w '%{http_code}' \
      --connect-timeout 3 --max-time 5 --http2-prior-knowledge -X POST \
      -H 'content-type: application/grpc' -H 'te: trailers' --data-binary @- '${url}'" 2>/dev/null)" || rc=$?
  printf '%s %s\n' "${rc}" "${status:-000}"
}

# Вызов неизвестного gRPC-метода через gateway: HTTP/2 без TLS, как у gRPC-клиента.
# Envoy отвечает на gRPC-запрос локальным ответом в gRPC-формате: HTTP 200 и grpc-status
# в заголовках (12 — UNIMPLEMENTED). Результат: "<curl exit code> <HTTP status> <grpc-status>".
grpc_unknown() {
  local result rc=0
  result="$(k -n "$1" exec deploy/net-probe -- \
    curl -sS -o /dev/null -w '%{http_code} %header{grpc-status}' --connect-timeout 3 --max-time 5 \
    --http2-prior-knowledge -X POST -H 'content-type: application/grpc' -H 'te: trailers' --data-binary '' \
    "${GATEWAY}/nomnom.unknown.v1.UnknownService/Call" 2>/dev/null)" || rc=$?
  # shellcheck disable=SC2086 # нормализация пробелов: при timeout grpc-status пуст
  printf '%s %s\n' "${rc}" "$(echo ${result:-000})"
}

# Вызов существующего Orders RPC с пустым protobuf request. До подключения
# production SubjectResolver backend ожидаемо возвращает UNAUTHENTICATED (16);
# этот ответ доказывает, что GRPCRoute дошёл до реального gRPC server.
grpc_orders() {
  local result rc=0
  result="$(k -n "$1" exec deploy/net-probe -- sh -c \
    "printf '\\000\\000\\000\\000\\000' | curl -sS -o /dev/null -w '%{http_code} %header{grpc-status}' \
      --connect-timeout 3 --max-time 5 --http2-prior-knowledge -X POST \
      -H 'content-type: application/grpc' -H 'te: trailers' --data-binary @- \
      '${GATEWAY}/nomnom.orders.v1.OrdersService/ListCustomerOrders'" 2>/dev/null)" || rc=$?
  # shellcheck disable=SC2086 # нормализация пробелов: при timeout grpc-status пуст
  printf '%s %s\n' "${rc}" "$(echo ${result:-000})"
}

# tcp <namespace> <host> <port> → "open" или "closed": TCP connect из net-probe под политиками
# этого неймспейса. Silent drop и отказ одинаково дают closed за 3 секунды; недоступный
# net-probe даёт пустой результат, а не closed.
tcp() {
  k -n "$1" exec deploy/net-probe -- sh -c "nc -z -w 3 '$2' '$3' >/dev/null 2>&1 && echo open || echo closed" \
    2>/dev/null || true
}

# Вызов ListCustomerOrders от nomnom-bff/grpc-caller с projected token (ADR 27) и
# customer_id=c1; дополнительные curl-аргументы — $1. Результат:
# "<curl exit code> <HTTP status> <grpc-status> <grpc-message>".
grpc_orders_authenticated() {
  local result rc=0
  result="$(k -n nomnom-bff exec deploy/grpc-caller -- sh -c \
    "printf '\\000\\000\\000\\000\\004\\012\\002c1' | curl -sS -o /dev/null \
      -w '%{http_code} %header{grpc-status} %header{grpc-message}' \
      --connect-timeout 3 --max-time 5 --http2-prior-knowledge -X POST \
      -H 'content-type: application/grpc' -H 'te: trailers' \
      -H \"authorization: Bearer \$(cat /var/run/secrets/nomnom/identity/nomnom.orders)\" ${1:-} \
      --data-binary @- '${GATEWAY}/nomnom.orders.v1.OrdersService/ListCustomerOrders'" 2>/dev/null)" || rc=$?
  # shellcheck disable=SC2086 # нормализация пробелов: при timeout grpc-status пуст
  printf '%s %s\n' "${rc}" "$(echo ${result:-000})"
}

resolves() {
  k -n "$1" exec deploy/net-probe -- nslookup "$2" >/dev/null 2>&1 && echo yes || echo no
}

hubble_drops() { # source pod, destination pod IP → число DROPPED flows за последние 5 минут
  local source="$1" destination="$2" total=0 count pod
  for pod in $(k -n kube-system get pods -l k8s-app=cilium -o name); do
    count="$(k -n kube-system exec "${pod}" -c cilium-agent -- \
      hubble observe --verdict DROPPED --since 5m --from-pod "${source}" --to-ip "${destination}" -o jsonpb 2>/dev/null |
      jq -s 'length')"
    total=$((total + ${count:-0}))
  done
  printf '%s\n' "${total}"
}

# --- Фаза A: без NetworkPolicy -----------------------------------------------------------
section "A. Без NetworkPolicy прямые вызовы проходят"
"${KIND_DIR}/scripts/policies.sh" delete >/dev/null
sleep 3
expect "A1 orders → restaurants напрямую" "0 200" "$(http nomnom-orders "${RESTAURANTS}")"
expect "A2 outsider → restaurants напрямую" "0 200" "$(http nomnom-outsider "${RESTAURANTS}")"
expect "A3 orders → gateway" "0 200 12" "$(grpc_unknown nomnom-orders)"

# --- Фаза B: с NetworkPolicy -------------------------------------------------------------
restarts_before="$(k get pods -A -o json |
  jq -c '[.items[] | {pod: (.metadata.namespace + "/" + .metadata.name), restarts: ([.status.containerStatuses[]?.restartCount] | add)}]')"
policy_applied_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
"${KIND_DIR}/scripts/policies.sh" apply >/dev/null
log "waiting for policy enforcement"
deadline=$((SECONDS + 60))
until [[ "$(http nomnom-orders "${RESTAURANTS}")" == "28 "* ]] || ((SECONDS >= deadline)); do
  sleep 2
done

section "1. Вызов бизнес-API через gateway"
expect "1.1 Orders RPC доходит до backend и требует identity" "0 200 16" \
  "$(grpc_orders nomnom-restaurants)"

section "2. Прямой вызов блокируется политикой (A/B)"
expect "2.1 условие: restaurants Available" True \
  "$(k -n nomnom-restaurants get deployment restaurants -o jsonpath='{.status.conditions[?(@.type=="Available")].status}')"
expect "2.2 условие: имя restaurants резолвится из orders" yes \
  "$(resolves nomnom-orders restaurants.nomnom-restaurants.svc.cluster.local)"
expect "2.3 условие: gateway доступен из orders в этом прогоне" "0 200 12" "$(grpc_unknown nomnom-orders)"
expect "2.4 Orders RPC через gateway проходит под политиками" "0 200 16" \
  "$(grpc_orders nomnom-restaurants)"
expect "2.5 orders → restaurants напрямую: silent drop (timeout, не DNS и не refused)" "28 000" \
  "$(http nomnom-orders "${RESTAURANTS}")"
expect "2.6 restaurants → orders напрямую: silent drop" "28 000" "$(http nomnom-restaurants "${ORDERS}")"
expect "2.7 outsider → restaurants: ingress backend закрыт" "28 000" "$(http nomnom-outsider "${RESTAURANTS}")"
expect "2.8 outsider → gateway: ingress gateway только из прикладных неймспейсов" "28 000" \
  "$(grpc_unknown nomnom-outsider)"
restaurants_ip="$(k -n nomnom-restaurants get pod -l app.kubernetes.io/name=restaurants,app.kubernetes.io/instance=restaurants -o jsonpath='{.items[0].status.podIP}')"
for namespace in nomnom-orders nomnom-outsider; do
  source_pod="${namespace}/$(k -n "${namespace}" get pod -l app.kubernetes.io/name=net-probe -o jsonpath='{.items[0].metadata.name}')"
  drops="$(hubble_drops "${source_pod}" "${restaurants_ip}")"
  if ((drops > 0)); then
    pass "2.9 Hubble: ${drops} DROPPED flows ${source_pod} → restaurants"
  else
    fail "2.9 Hubble: нет DROPPED flows ${source_pod} → restaurants"
  fi
done

section "3. Неизвестный маршрут"
expect "3.1 неизвестный gRPC-сервис через gateway: grpc-status 12 (UNIMPLEMENTED)" "0 200 12" \
  "$(grpc_unknown nomnom-restaurants)"

section "4–5. Metadata, status, trace context, deadlines, retries"
# Без token backend отвечает UNAUTHENTICATED (1.1); с token — ответ SpiceDB: authorization
# metadata дошла до backend, grpc-status и grpc-message backend'а дошли до вызывающего.
expect "4.1 authorization metadata через gateway: PERMISSION_DENIED от SpiceDB" \
  "0 200 7 permission denied: service:bff view_orders on customer:c1" "$(grpc_orders_authenticated)"
# Trace id и span id вызывающего случайны на каждый прогон: server span orders в приёмнике
# trace-capture должен принадлежать тому же trace и иметь parent — span вызывающего.
trace_id="$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')"
parent_id="$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')"
grpc_orders_authenticated "-H 'traceparent: 00-${trace_id}-${parent_id}-01'" >/dev/null
captured=no
for _ in $(seq 1 20); do
  # BatchSpanProcessor отправляет spans раз в 5 секунд.
  if k -n nomnom-observability logs deploy/trace-capture --since=2m 2>/dev/null |
    grep -A2 "Trace ID *: ${trace_id}" | grep -q "Parent ID *: ${parent_id}"; then
    captured=yes
    break
  fi
  sleep 1
done
expect "4.2 traceparent вызывающего продолжен server span orders через gateway" yes "${captured}"
# Истёкший deadline gRPC server orders завершает одним из двух способов: статусом
# DEADLINE_EXCEEDED, если handler успел ответить, или RST_STREAM до headers, который Envoy
# отдаёт как UNAVAILABLE (remote reset). Обычный gRPC-клиент сам соблюдает deadline и в
# обоих случаях вернёт DEADLINE_EXCEEDED; curl deadline не отслеживает. Оба исхода
# формирует backend: route timeout gateway (300s) за 1 мкс не срабатывает, а тот же вызов
# без grpc-timeout получает PERMISSION_DENIED (4.1).
deadline_result="$(grpc_orders_authenticated "-H 'grpc-timeout: 1u'")"
case "${deadline_result}" in
  "0 200 4 context deadline exceeded" | "0 200 14 "*"reset reason: remote reset")
    pass "5.1 deadline вызывающего (grpc-timeout 1u) соблюдается backend'ом: ${deadline_result}" ;;
  *) fail "5.1 deadline вызывающего (grpc-timeout 1u) соблюдается backend'ом (got '${deadline_result}')" ;;
esac
# Retries (ADR 15): идемпотентные чтения orders-v1-reads повторяются не более 2 раз на
# UNAVAILABLE, команды orders-v1 не повторяются. Отказ зависимости — Cilium deny orders →
# SpiceDB; повторы считаются по статистике Envoy (upstream_rq_retry кластера route).
envoy_retries() { # route → upstream_rq_retry
  curl -s "localhost:19100/stats?filter=grpcroute/nomnom-gateway/$1/rule/0.upstream_rq_retry\$" |
    awk -F': ' '{print $2}'
}
envoy_pod="$(k -n "${GATEWAY_SYSTEM_NAMESPACE}" get pod -l gateway.envoyproxy.io/owning-gateway-name=internal \
  -o jsonpath='{.items[0].metadata.name}')"
k -n "${GATEWAY_SYSTEM_NAMESPACE}" port-forward "pod/${envoy_pod}" 19100:19000 >/dev/null 2>&1 &
port_forward=$!
sleep 2
k apply -f "${KIND_DIR}/manifests/fixtures/deny-orders-spicedb.yaml" >/dev/null
sleep 3
reads_before="$(envoy_retries orders-v1-reads)"
commands_before="$(envoy_retries orders-v1)"
expect "5a.1 ListCustomerOrders при недоступном SpiceDB: UNAVAILABLE" \
  "0 200 14 dependency unavailable" "$(grpc_orders_authenticated "--max-time 20")"
expect "5a.2 чтение повторено gateway ровно 2 раза" 2 \
  "$(( $(envoy_retries orders-v1-reads) - reads_before ))"
# RequestOrderCancellation{order_id: o1, request_id: r1}: команда с той же причиной отказа.
cancellation="$(k -n nomnom-bff exec deploy/grpc-caller -- sh -c \
  "printf '\\000\\000\\000\\000\\010\\012\\002o1\\022\\002r1' | curl -sS -o /dev/null \
    -w '%{http_code} %header{grpc-status} %header{grpc-message}' --max-time 20 \
    --http2-prior-knowledge -X POST -H 'content-type: application/grpc' -H 'te: trailers' \
    -H \"authorization: Bearer \$(cat /var/run/secrets/nomnom/identity/nomnom.orders)\" \
    --data-binary @- '${GATEWAY}/nomnom.orders.v1.OrdersService/RequestOrderCancellation'" 2>/dev/null)"
expect "5a.3 RequestOrderCancellation при недоступном SpiceDB: UNAVAILABLE" \
  "200 14 dependency unavailable" "${cancellation}"
expect "5a.4 команда не повторяется gateway" 0 "$(( $(envoy_retries orders-v1) - commands_before ))"
k delete -f "${KIND_DIR}/manifests/fixtures/deny-orders-spicedb.yaml" >/dev/null
kill "${port_forward}" 2>/dev/null || true

section "6. Инфраструктурные подключения под политиками"
expect "6.1 DNS из orders под политиками" yes "$(resolves nomnom-orders kubernetes.default.svc.cluster.local)"
POSTGRES_ORDERS=postgres-orders.nomnom-data.svc.cluster.local
KAFKA=kafka.nomnom-infra.svc.cluster.local
TEMPORAL=temporal.nomnom-infra.svc.cluster.local
expect "6.2 orders → PostgreSQL orders" open "$(tcp nomnom-orders "${POSTGRES_ORDERS}" 5432)"
expect "6.3 orders → Kafka" open "$(tcp nomnom-orders "${KAFKA}" 9092)"
expect "6.4 orders → Temporal" open "$(tcp nomnom-orders "${TEMPORAL}" 7233)"
for namespace in nomnom-restaurants nomnom-delivery nomnom-payments; do
  expect "6.5 ${namespace} → Kafka" open "$(tcp "${namespace}" "${KAFKA}" 9092)"
done
expect "6.6 restaurants → PostgreSQL orders: БД доступна только владельцу" closed \
  "$(tcp nomnom-restaurants "${POSTGRES_ORDERS}" 5432)"
expect "6.7 payments → Temporal: Temporal доступен только orders" closed \
  "$(tcp nomnom-payments "${TEMPORAL}" 7233)"
# Приложения падают на старте без обязательной инфраструктуры: Available под политиками
# означает реальное подключение orders и orders-consumer к PostgreSQL, Kafka и Temporal.
for deployment in orders orders-consumer; do
  expect "6.8 ${deployment} Available под политиками" True \
    "$(k -n nomnom-orders get deployment "${deployment}" \
      -o jsonpath='{.status.conditions[?(@.type=="Available")].status}')"
done
blocked "6.9 MongoDB restaurants не развёрнута: restaurants её ещё не использует"
if k -n nomnom-observability get service alloy >/dev/null 2>&1; then
  expect "6.10 orders → Alloy OTLP" open "$(tcp nomnom-orders alloy.nomnom-observability.svc.cluster.local 4317)"
else
  blocked "6.10 Alloy развёрнут только с GRAFANA_CLOUD=1"
fi

section "6a. Kubelet-пробы и системные компоненты под политиками"
sleep 20
for namespace in "${APP_NAMESPACES[@]}" "${GATEWAY_SYSTEM_NAMESPACE}" kube-system; do
  if [[ -n "$(k -n "${namespace}" get deployment -o name)" ]]; then
    if k -n "${namespace}" wait deployment --all --for=condition=Available --timeout=60s >/dev/null; then
      pass "6a.1 deployments ${namespace} Available"
    else
      fail "6a.1 deployments ${namespace} Available"
    fi
  fi
done
restarts_after="$(k get pods -A -o json |
  jq -c '[.items[] | {pod: (.metadata.namespace + "/" + .metadata.name), restarts: ([.status.containerStatuses[]?.restartCount] | add)}]')"
grown="$(jq -n --argjson before "${restarts_before}" --argjson after "${restarts_after}" \
  '[$after[] | . as $p | select(($before | map(select(.pod == $p.pod)) | .[0].restarts // $p.restarts) != $p.restarts) | .pod]')"
expect "6a.2 рестарты pod не выросли после применения политик" "[]" "$(jq -c . <<<"${grown}")"
unhealthy="$(k get events -A --field-selector reason=Unhealthy -o json |
  jq --arg since "${policy_applied_at}" '[.items[] | select((.lastTimestamp // .eventTime // "") > $since)] | length')"
expect "6a.3 нет проваленных kubelet-проб после применения политик (Envoy Proxy, CoreDNS, Cilium)" 0 "${unhealthy}"
# Runtime contract (ADR 17): startup/liveness/readiness на техническом порту metrics,
# deployments при этом Available (6a.1) и без проваленных проб (6a.3).
for app in orders restaurants delivery payments; do
  probes="$(k -n "nomnom-${app}" get deployment "${app}" -o json |
    jq -c '.spec.template.spec.containers[0] | [
      .startupProbe.httpGet.path, .livenessProbe.httpGet.path, .readinessProbe.httpGet.path,
      .startupProbe.httpGet.port, .livenessProbe.httpGet.port, .readinessProbe.httpGet.port]')"
  expect "6a.4 probes ${app} на /health/* порта metrics" \
    '["/health/startup","/health/live","/health/ready","metrics","metrics","metrics"]' "${probes}"
done

section "E2E через публичный API BFF"
blocked "образ BFF не определён (ADR 10); NodePort 30080 → 127.0.0.1:${BFF_HOST_PORT} подготовлен"

printf '\nfailures: %s, report: %s\n' "${failures}" "${REPORT}" >&2
((failures == 0))
