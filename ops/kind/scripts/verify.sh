#!/usr/bin/env bash
# Проверки схемы «синхронные вызовы бизнес-API только через internal gateway».
# Фаза A выполняется без NetworkPolicy, фаза B — с ними; по завершении политики остаются.
# Проверки, которым нужна ещё не развёрнутая инфраструктура, выводятся как BLOCKED.
source "$(dirname "$0")/lib.sh"
source "$(dirname "$0")/probe.sh"

require_command kubectl jq python3 openssl
assert_test_context

# Прямые вызовы реальных gRPC-методов backend'ов в обход gateway (h2c, бизнес-порт grpc).
RESTAURANTS="http://restaurants.nomnom-restaurants.svc.cluster.local:8082/nomnom.restaurants.v1.RestaurantsService/GetRestaurant"
ORDERS="http://orders.nomnom-orders.svc.cluster.local:8081/nomnom.orders.v1.OrdersService/ListCustomerOrders"
GATEWAY="http://${INTERNAL_GATEWAY_ADDR}"
UNKNOWN_SERVICE="${GATEWAY}/nomnom.unknown.v1.UnknownService/Call"
ORDERS_VIA_GATEWAY="${GATEWAY}/nomnom.orders.v1.OrdersService/ListCustomerOrders"
# ListCustomerOrdersRequest{customer_id: "c1"} и RequestOrderCancellationRequest{order_id: "o1",
# request_id: "r1"} в формате printf.
CUSTOMER_C1='\012\002c1'
CANCELLATION_O1='\012\002o1\022\002r1'
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
expect "A1 orders → restaurants напрямую" "0 5 Restaurant  was not found" "$(probe_grpc nomnom-orders/net-probe "${RESTAURANTS}")"
expect "A2 outsider → restaurants напрямую" "0 5 Restaurant  was not found" "$(probe_grpc nomnom-outsider/net-probe "${RESTAURANTS}")"
expect "A3 orders → gateway" "0 12" "$(probe_grpc nomnom-orders/net-probe "${UNKNOWN_SERVICE}")"

# --- Фаза B: с NetworkPolicy -------------------------------------------------------------
restarts_before="$(k get pods -A -o json |
  jq -c '[.items[] | {pod: (.metadata.namespace + "/" + .metadata.name), restarts: ([.status.containerStatuses[]?.restartCount] | add)}]')"
policy_applied_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
"${KIND_DIR}/scripts/policies.sh" apply >/dev/null
log "waiting for policy enforcement"
deadline=$((SECONDS + 60))
until [[ "$(probe_grpc nomnom-orders/net-probe "${RESTAURANTS}")" == "28 "* ]] || ((SECONDS >= deadline)); do
  sleep 2
done

section "1. Вызов бизнес-API через gateway"
expect "1.1 Orders RPC доходит до backend и требует identity" "0 16 caller identity is not authenticated" \
  "$(probe_grpc nomnom-restaurants/net-probe "${ORDERS_VIA_GATEWAY}")"

# Команды Order Fulfillment (orders-worker) опубликованы точными методами. Token
# grpc-caller выпущен для audience nomnom.orders, поэтому backend отвечает
# UNAUTHENTICATED (16) — ответ backend'а, а не UNIMPLEMENTED (12) от Envoy.
for method in ReceiveOrderRequest CancelRestaurantOrder; do
  expect "1.2 restaurants ${method} через gateway доходит до backend" \
    "0 16 verified caller identity is required" \
    "$(probe_grpc nomnom-bff/grpc-caller "${GATEWAY}/nomnom.restaurants.v1.RestaurantsService/${method}")"
done
for method in CreateDelivery ReplanDelivery CancelDelivery; do
  expect "1.3 delivery ${method} через gateway доходит до backend" \
    "0 16 verified caller identity is required" \
    "$(probe_grpc nomnom-bff/grpc-caller "${GATEWAY}/nomnom.delivery.v1.DeliveryService/${method}")"
done
for method in CreatePayment AuthorizePayment CapturePayment CancelAuthorization RefundPayment; do
  expect "1.4 payments ${method} через gateway доходит до backend" "0 16 invalid caller identity" \
    "$(probe_grpc nomnom-bff/grpc-caller "${GATEWAY}/nomnom.payments.v1.PaymentsService/${method}")"
done
expect "1.5 неопубликованная команда кухни (AcceptRestaurantOrder): UNIMPLEMENTED от gateway" "0 12" \
  "$(probe_grpc nomnom-bff/grpc-caller "${GATEWAY}/nomnom.restaurants.v1.RestaurantsService/AcceptRestaurantOrder")"

section "2. Прямой вызов блокируется политикой (A/B)"
expect "2.1 условие: restaurants Available" True \
  "$(k -n nomnom-restaurants get deployment restaurants -o jsonpath='{.status.conditions[?(@.type=="Available")].status}')"
expect "2.2 условие: имя restaurants резолвится из orders" yes \
  "$(probe_dns nomnom-orders restaurants.nomnom-restaurants.svc.cluster.local)"
expect "2.3 условие: gateway доступен из orders в этом прогоне" "0 12" "$(probe_grpc nomnom-orders/net-probe "${UNKNOWN_SERVICE}")"
expect "2.4 Orders RPC через gateway проходит под политиками" "0 16 caller identity is not authenticated" \
  "$(probe_grpc nomnom-restaurants/net-probe "${ORDERS_VIA_GATEWAY}")"
expect "2.5 orders → restaurants напрямую: silent drop (timeout, не DNS и не refused)" "28" \
  "$(probe_grpc nomnom-orders/net-probe "${RESTAURANTS}")"
expect "2.6 restaurants → orders напрямую: silent drop" "28" "$(probe_grpc nomnom-restaurants/net-probe "${ORDERS}")"
expect "2.7 outsider → restaurants: ingress backend закрыт" "28" "$(probe_grpc nomnom-outsider/net-probe "${RESTAURANTS}")"
expect "2.8 outsider → gateway: ingress gateway только из прикладных неймспейсов" "28" \
  "$(probe_grpc nomnom-outsider/net-probe "${UNKNOWN_SERVICE}")"
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
expect "3.1 неизвестный gRPC-сервис через gateway: grpc-status 12 (UNIMPLEMENTED)" "0 12" \
  "$(probe_grpc nomnom-restaurants/net-probe "${UNKNOWN_SERVICE}")"

section "4–5. Metadata, status, trace context, deadlines, retries"
# Без token backend отвечает UNAUTHENTICATED (1.1); с token — ответ SpiceDB: authorization
# metadata дошла до backend, grpc-status и grpc-message backend'а дошли до вызывающего.
expect "4.1 authorization metadata через gateway: PERMISSION_DENIED от SpiceDB" \
  "0 7 permission denied: service:grpc-caller view_orders on customer:c1" "$(probe_grpc nomnom-bff/grpc-caller "${ORDERS_VIA_GATEWAY}" "${CUSTOMER_C1}")"
# Trace id и span id вызывающего случайны на каждый прогон: server span orders в приёмнике
# trace-capture должен принадлежать тому же trace и иметь parent — span вызывающего.
trace_id="$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')"
parent_id="$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')"
probe_grpc nomnom-bff/grpc-caller "${ORDERS_VIA_GATEWAY}" "${CUSTOMER_C1}" -H "'traceparent: 00-${trace_id}-${parent_id}-01'" >/dev/null
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
deadline_result="$(probe_grpc nomnom-bff/grpc-caller "${ORDERS_VIA_GATEWAY}" "${CUSTOMER_C1}" -H "'grpc-timeout: 1u'")"
case "${deadline_result}" in
  "0 4 context deadline exceeded" | "0 14 "*"reset reason: remote reset")
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
  "0 14 dependency unavailable" "$(probe_grpc nomnom-bff/grpc-caller "${ORDERS_VIA_GATEWAY}" "${CUSTOMER_C1}" --max-time 20)"
expect "5a.2 чтение повторено gateway ровно 2 раза" 2 \
  "$(( $(envoy_retries orders-v1-reads) - reads_before ))"
# RequestOrderCancellation{order_id: o1, request_id: r1}: команда с той же причиной отказа.
cancellation="$(probe_grpc nomnom-bff/grpc-caller \
  "${GATEWAY}/nomnom.orders.v1.OrdersService/RequestOrderCancellation" "${CANCELLATION_O1}" --max-time 20)"
expect "5a.3 RequestOrderCancellation при недоступном SpiceDB: UNAVAILABLE" \
  "0 14 dependency unavailable" "${cancellation}"
expect "5a.4 команда не повторяется gateway" 0 "$(( $(envoy_retries orders-v1) - commands_before ))"
k delete -f "${KIND_DIR}/manifests/fixtures/deny-orders-spicedb.yaml" >/dev/null
kill "${port_forward}" 2>/dev/null || true

section "6. Инфраструктурные подключения под политиками"
expect "6.1 DNS из orders под политиками" yes "$(probe_dns nomnom-orders kubernetes.default.svc.cluster.local)"
POSTGRES_ORDERS=postgres-orders.nomnom-data.svc.cluster.local
KAFKA=kafka.nomnom-infra.svc.cluster.local
TEMPORAL=temporal.nomnom-infra.svc.cluster.local
expect "6.2 orders → PostgreSQL orders" open "$(probe_tcp nomnom-orders "${POSTGRES_ORDERS}" 5432)"
expect "6.3 orders → Kafka" open "$(probe_tcp nomnom-orders "${KAFKA}" 9092)"
expect "6.4 orders → Temporal" open "$(probe_tcp nomnom-orders "${TEMPORAL}" 7233)"
for namespace in nomnom-restaurants nomnom-delivery nomnom-payments; do
  expect "6.5 ${namespace} → Kafka" open "$(probe_tcp "${namespace}" "${KAFKA}" 9092)"
done
expect "6.6 restaurants → PostgreSQL orders: БД доступна только владельцу" closed \
  "$(probe_tcp nomnom-restaurants "${POSTGRES_ORDERS}" 5432)"
expect "6.7 payments → Temporal: Temporal доступен только orders" closed \
  "$(probe_tcp nomnom-payments "${TEMPORAL}" 7233)"
# Приложения падают на старте без обязательной инфраструктуры: Available под политиками
# означает реальное подключение orders и orders-consumer к PostgreSQL, Kafka и Temporal.
for deployment in orders orders-consumer; do
  expect "6.8 ${deployment} Available под политиками" True \
    "$(k -n nomnom-orders get deployment "${deployment}" \
      -o jsonpath='{.status.conditions[?(@.type=="Available")].status}')"
done
blocked "6.9 MongoDB restaurants не развёрнута: restaurants её ещё не использует"
if k -n nomnom-observability get service alloy >/dev/null 2>&1; then
  expect "6.10 orders → Alloy OTLP" open "$(probe_tcp nomnom-orders alloy.nomnom-observability.svc.cluster.local 4317)"
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

section "7. BFF насквозь: access token → user assertion → orders (ADR 32)"
# BFF доступен проверке только через port-forward: наружу он не опубликован, а egress BFF
# ограничен DNS и internal gateway (app-baseline.yaml). Access token выпускает тестовый
# identity provider (ключ tests/identity/fixtures, только для kind).
BFF_FORWARD_PORT="${BFF_FORWARD_PORT:-18090}"
fixtures="${REPO_ROOT}/tests/identity/fixtures"
mint() { python3 -I "${KIND_DIR}/scripts/mint-jwt.py" "$@"; }
# graphql <query> [access token] → тело ответа BFF.
graphql() {
  local auth=()
  [[ -n "${2:-}" ]] && auth=(-H "authorization: Bearer $2")
  curl -sS --max-time 10 "http://127.0.0.1:${BFF_FORWARD_PORT}/graphql" -H 'content-type: application/json' \
    ${auth[@]+"${auth[@]}"} -d "$(jq -cn --arg q "$1" '{query: $q}')"
}
# Из grpc-caller: тот же неймспейс nomnom-bff и те же политики, что у BFF.
bff_tcp() {
  k -n nomnom-bff exec deploy/grpc-caller -- sh -c \
    "nc -z -w 3 '$1' '$2' >/dev/null 2>&1 && echo open || echo closed" 2>/dev/null || true
}
expect "7.1 BFF Available" True \
  "$(k -n nomnom-bff get deployment bff -o jsonpath='{.status.conditions[?(@.type=="Available")].status}')"
expect "7.2 probes BFF на /health/* Router" '["/health/live","/health/ready"]' \
  "$(k -n nomnom-bff get deployment bff -o json |
    jq -c '.spec.template.spec.containers[0] | [.livenessProbe.httpGet.path, .readinessProbe.httpGet.path]')"
expect "7.3 nomnom-bff → orders напрямую закрыт" closed "$(bff_tcp orders.nomnom-orders.svc.cluster.local 8081)"
expect "7.4 nomnom-bff → PostgreSQL orders закрыт" closed "$(bff_tcp "${POSTGRES_ORDERS}" 5432)"
k -n nomnom-bff port-forward svc/bff "${BFF_FORWARD_PORT}:3010" >/dev/null 2>&1 &
bff_forward=$!
for _ in $(seq 1 20); do
  curl -fsS "http://127.0.0.1:${BFF_FORWARD_PORT}/health/ready" >/dev/null 2>&1 && break
  sleep 1
done
e2e_started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
customer="kind-e2e-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
missing_order="$(python3 -I -c 'import uuid; print(uuid.uuid4())')"
token="$(mint access-token "${fixtures}/idp-signing-key.pem" "${fixtures}/idp-jwks.json" \
  "${fixtures}/idp-vectors.json" "${customer}")"
order_query="{ order(id: \"${missing_order}\") { id status } }"
expect "7.5 bffInfo без access token" '{"data":{"bffInfo":{"name":"nomnom-bff","version":"0.1.0"}}}' \
  "$(graphql '{ bffInfo { name version } }' | jq -c .)"
expect "7.6 order(id) без Authorization: ошибка аутентификации (AC-576)" \
  '{"order":null,"codes":["Unauthenticated"]}' \
  "$(graphql "${order_query}" | jq -c '{order: .data.order, codes: [.errors[]?.extensions.errors[]?.extensions.code]}')"
expect "7.7 order(id) отсутствующего заказа с access token: null без ошибок (AC-575)" \
  '{"data":{"order":null}}' "$(graphql "${order_query}" "${token}" | jq -c .)"
kill "${bff_forward}" 2>/dev/null || true
# orders_log <pattern> → yes, если строка появилась в log orders после начала раздела.
orders_log() {
  local _
  for _ in $(seq 1 10); do
    if k -n nomnom-orders logs deploy/orders --since-time="${e2e_started_at}" 2>/dev/null | grep -qF "$1"; then
      echo yes
      return
    fi
    sleep 1
  done
  echo no
}
expect "7.8 orders принял user assertion BFF как Subject{user} (log)" yes \
  "$(orders_log "user assertion accepted: delegate=bff")"
# Тот же user assertion (ключ BFF из Secret, aud nomnom.orders, sub покупателя) от другого
# workload: nomnom-bff/grpc-caller допущен allowlist orders, но не делегат.
assertion_key="$(mktemp)"
k -n nomnom-bff get secret bff-assertion-key -o jsonpath='{.data.key\.pem}' | base64 -d >"${assertion_key}"
assertion="$(mint user-assertion "${assertion_key}" nomnom-bff-kind nomnom.orders "${customer}")"
rm -f "${assertion_key}"
# GetOrderRequest{order_id: UUID}: tag 0x0a, длина 36 (0x24); сообщение 38 байт (0x26).
replay="$(k -n nomnom-bff exec deploy/grpc-caller -- sh -c \
  "printf '\\000\\000\\000\\000\\046\\012\\044%s' '${missing_order}' | curl -sS -o /dev/null \
    -w '%{http_code} %header{grpc-status}' --max-time 5 --http2-prior-knowledge -X POST \
    -H 'content-type: application/grpc' -H 'te: trailers' \
    -H \"authorization: Bearer \$(cat /var/run/secrets/nomnom/identity/nomnom.orders)\" \
    -H 'x-nomnom-user-assertion: ${assertion}' \
    --data-binary @- '${GATEWAY}/nomnom.orders.v1.OrdersService/GetOrder'" 2>/dev/null)" || true
expect "7.9 тот же user assertion от другого workload: UNAUTHENTICATED" "200 16" "${replay}"
expect "7.10 orders отклонил assertion: grpc-caller не делегат (log)" yes \
  "$(orders_log "user assertion from grpc-caller: service is not an allowed delegate")"

printf '\nfailures: %s, report: %s\n' "${failures}" "${REPORT}" >&2
((failures == 0))
