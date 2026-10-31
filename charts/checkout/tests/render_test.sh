#!/usr/bin/env bash
# Chart render assertions for checkout: Postgres, Kafka and the five sibling
# services are the only runtime, so the mandatory configuration has no silent
# default, no *_MODE is ever rendered, the pod is hardened and has room to shut
# down, and the env cmd/api reads is the env the chart renders.
# Run: bash charts/checkout/tests/render_test.sh   (needs helm)
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "$chart_dir/../.." && pwd)"
release=checkout
# Dummy values, no password: the chart refuses to render without a database
# source, Kafka brokers and the five sibling base URLs. None is ever dialled.
siblings=(shoppingCartUrl:http://shopping-cart.example.invalid
  deliveryPromiseUrl:http://delivery-promise.example.invalid
  pricingUrl:http://pricing.example.invalid
  customerPaymentsUrl:http://customer-payments.example.invalid
  customerOrderingUrl:http://customer-ordering.example.invalid)
base=(--set database.url=postgres://u@example.invalid:5432/db
  --set kafka.brokers=kafka.example.invalid:9092)
for s in "${siblings[@]}"; do base+=(--set "config.${s%%:*}=${s#*:}"); done
# Every optional value set, so every env var cmd/api can read is rendered.
everything=(--set a2a.enabled=true --set config.siblingTimeout=3s --set config.customerOrderingTimeout=10s
  --set config.indoubtSweepInterval=10s --set config.stuckSweepInterval=20s
  --set config.voidSweepInterval=30s --set config.expirySweepInterval=40s
  --set config.purgeSweepInterval=2h --set config.outboxRelayInterval=2s
  --set config.shutdownDrainDelay=5s --set database.migrationsUrl=postgres://u@example.invalid:5432/direct
  --set autoscaling.api.enabled=true --set ingress.enabled=true --set gatewayApi.enabled=true)

fail() { echo "FAIL: $*" >&2; exit 1; }
command -v helm >/dev/null || fail "helm is not installed"

configmap="$(helm template "$release" "$chart_dir" "${base[@]}" --show-only templates/configmap.yaml)"
api_deployment="$(helm template "$release" "$chart_dir" "${base[@]}" --show-only templates/deployment.yaml)"
configmap_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap")"
[[ -n "$configmap_name" ]] || fail "could not read the ConfigMap name"

# Every non-secret config value is rendered into the chart ConfigMap, and the api
# Deployment must mount it with envFrom, or a value that is only in the
# ConfigMap silently does nothing.
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q 'configMapRef:' \
  || fail "api Deployment does not mount the ConfigMap via envFrom"
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q "name: $configmap_name" \
  || fail "api Deployment envFrom does not reference ConfigMap $configmap_name"
grep -q 'checksum/config:' <<<"$api_deployment" || fail "the pods must roll when the ConfigMap changes (checksum/config)"

# A tuned config.* value reaches the ConfigMap the api mounts and the container env.
tuned="$(helm template "$release" "$chart_dir" "${base[@]}" --set config.outboxRelayInterval=7s --set config.outboxMaxAttempts=5 \
  --set config.siblingTimeout=2s --set config.voidSweepInterval=45s --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_RELAY_INTERVAL: "7s"' <<<"$tuned" || fail "config.outboxRelayInterval is not rendered into the ConfigMap"
grep -q 'OUTBOX_MAX_ATTEMPTS: "5"' <<<"$tuned" || fail "config.outboxMaxAttempts is not rendered into the ConfigMap"
grep -q 'SIBLING_TIMEOUT: "2s"' <<<"$tuned" || fail "config.siblingTimeout is not rendered into the ConfigMap"
grep -q 'VOID_SWEEP_INTERVAL: "45s"' <<<"$tuned" || fail "config.voidSweepInterval is not rendered into the ConfigMap"

# The relay attempt cap defaults to 20 and 0 (unlimited) is rendered as is.
grep -q 'OUTBOX_MAX_ATTEMPTS: "20"' <<<"$configmap" || fail "OUTBOX_MAX_ATTEMPTS must default to 20"
grep -A1 'name: OUTBOX_MAX_ATTEMPTS' <<<"$api_deployment" | grep -q 'value: "20"' \
  || fail "api Deployment does not set OUTBOX_MAX_ATTEMPTS"
unlimited="$(helm template "$release" "$chart_dir" "${base[@]}" --set config.outboxMaxAttempts=0 --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_MAX_ATTEMPTS: "0"' <<<"$unlimited" || fail "config.outboxMaxAttempts=0 is not rendered"
zero_drain="$(helm template "$release" "$chart_dir" "${base[@]}" --set config.shutdownDrainDelay=0 --show-only templates/configmap.yaml)"
grep -q 'SHUTDOWN_DRAIN_DELAY: "0"' <<<"$zero_drain" || fail "config.shutdownDrainDelay=0 is not rendered"

# Chart vs binary: every env var cmd/api reads must be rendered into the
# Deployment when every optional value is set. The names come from the binary's
# own os.Getenv / getenv("...") / env("...") literals and from the literals read
# through a variable (the five sibling *_URL names of the siblingURLs table and
# the five *_SWEEP_INTERVAL names of the scheduler table), so a new variable in
# cmd/api fails this test until the chart renders it.
binary_env() {
  {
    grep -ohE '(Getenv|getenv|env)\("[A-Z][A-Z0-9_]*"' "$repo_dir"/cmd/api/*.go --exclude='*_test.go' | sed -E 's/.*\("//; s/"$//'
    grep -ohE '"[A-Z][A-Z0-9_]*(_URL|_SWEEP_INTERVAL)"' "$repo_dir"/cmd/api/*.go --exclude='*_test.go' | tr -d '"'
  } | sort -u
}
# Word splitting is safe: the names are [A-Z0-9_] only (bash 3.2 has no mapfile).
# shellcheck disable=SC2207
api_vars=($(binary_env))
(( ${#api_vars[@]} >= 18 )) || fail "found only ${#api_vars[@]} env names in cmd/api; the scan is looking in the wrong place"
full_deployment="$(helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}" --show-only templates/deployment.yaml)"
for var in "${api_vars[@]}" DATABASE_URL MIGRATIONS_DATABASE_URL KAFKA_BROKERS \
  SHOPPING_CART_URL DELIVERY_PROMISE_URL PRICING_URL CUSTOMER_PAYMENTS_URL CUSTOMER_ORDERING_URL \
  SIBLING_TIMEOUT CUSTOMER_ORDERING_TIMEOUT \
  INDOUBT_SWEEP_INTERVAL STUCK_SWEEP_INTERVAL VOID_SWEEP_INTERVAL EXPIRY_SWEEP_INTERVAL PURGE_SWEEP_INTERVAL \
  HTTP_ADDR OUTBOX_RELAY_INTERVAL OUTBOX_MAX_ATTEMPTS SHUTDOWN_DRAIN_DELAY LOG_LEVEL; do
  grep -q "name: $var\$" <<<"$full_deployment" || fail "the api Deployment does not render $var, which cmd/api reads"
done

# Every key the ConfigMap renders is a variable cmd/api reads (no dead keys), and
# the ConfigMap never carries a DSN.
full_configmap="$(helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}" --show-only templates/configmap.yaml)"
api_known=" ${api_vars[*]} "
while read -r key; do
  [[ "$api_known" == *" $key "* ]] || fail "ConfigMap key $key is read by no binary"
done < <(awk '/^data:/ {d=1; next} d && /^  [A-Z]/ {sub(/:.*/, ""); sub(/^  /, ""); print}' <<<"$full_configmap")
if grep -qE '^  (MIGRATIONS_)?DATABASE_URL:' <<<"$full_configmap"; then fail "a DSN must never land in the ConfigMap"; fi

# The mandatory configuration is rendered from values.
env_value() { # env_value <rendered> <NAME>
  grep -A1 "name: $2\$" <<<"$1" | grep -m1 'value:' | sed -E 's/.*value: "?//; s/"$//'
}
[[ "$(env_value "$api_deployment" KAFKA_BROKERS)" == "kafka.example.invalid:9092" ]] || fail "KAFKA_BROKERS is not rendered from kafka.brokers"
grep -q '^  KAFKA_BROKERS: "kafka.example.invalid:9092"' <<<"$configmap" || fail "the ConfigMap does not carry KAFKA_BROKERS"
for kv in 'SHOPPING_CART_URL=http://shopping-cart.example.invalid' 'DELIVERY_PROMISE_URL=http://delivery-promise.example.invalid' \
  'PRICING_URL=http://pricing.example.invalid' 'CUSTOMER_PAYMENTS_URL=http://customer-payments.example.invalid' \
  'CUSTOMER_ORDERING_URL=http://customer-ordering.example.invalid'; do
  [[ "$(env_value "$api_deployment" "${kv%%=*}")" == "${kv#*=}" ]] || fail "the api Deployment does not render $kv"
  grep -q "^  ${kv%%=*}: \"${kv#*=}\"" <<<"$configmap" || fail "the ConfigMap does not carry $kv"
done
# The sibling URLs come from values and do not change with the release name.
other="$(helm template another-release "$chart_dir" "${base[@]}" --show-only templates/deployment.yaml)"
for kv in 'SHOPPING_CART_URL=http://shopping-cart.example.invalid' 'CUSTOMER_ORDERING_URL=http://customer-ordering.example.invalid'; do
  [[ "$(env_value "$other" "${kv%%=*}")" == "${kv#*=}" ]] || fail "${kv%%=*} must not depend on the release name"
done

# DATABASE_URL comes from a Secret; MIGRATIONS_DATABASE_URL is optional.
grep -A4 'name: DATABASE_URL$' <<<"$api_deployment" | grep -q 'secretKeyRef:' || fail "DATABASE_URL must come from a Secret (secretKeyRef)"
grep -A6 'name: MIGRATIONS_DATABASE_URL$' <<<"$api_deployment" | grep -q 'secretKeyRef:' || fail "MIGRATIONS_DATABASE_URL must come from a Secret (secretKeyRef)"
grep -A6 'name: MIGRATIONS_DATABASE_URL$' <<<"$api_deployment" | grep -q 'optional: true' || fail "MIGRATIONS_DATABASE_URL must be optional: true"
grep -A6 'name: MIGRATIONS_DATABASE_URL$' <<<"$api_deployment" | grep -q 'key: MIGRATIONS_DATABASE_URL' \
  || fail "MIGRATIONS_DATABASE_URL must read database.migrationsExistingSecretKey"
existing="$(helm template "$release" "$chart_dir" --set database.existingSecret=my-db "${base[@]:2}" --show-only templates/deployment.yaml)"
grep -A4 'name: DATABASE_URL$' <<<"$existing" | grep -q 'name: my-db' || fail "database.existingSecret must be the DATABASE_URL Secret"
no_secret="$(helm template "$release" "$chart_dir" --set database.existingSecret=my-db "${base[@]:2}")"
if grep -q 'kind: Secret' <<<"$no_secret"; then fail "no Secret may be created when database.existingSecret is set"; fi
helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}" | grep -q 'kind: Secret' || fail "database.url must create the Secret"

# Pod hardening, probes, the image tag and shutdown room.
for want in 'runAsNonRoot: true' 'readOnlyRootFilesystem: true' 'allowPrivilegeEscalation: false' 'type: RuntimeDefault' 'path: /healthz' 'path: /readyz'; do
  grep -q -- "$want" <<<"$api_deployment" || fail "the api Deployment lacks '$want'"
done
grep -A2 'drop:' <<<"$api_deployment" | grep -q 'ALL' || fail "the api container must drop ALL capabilities"
grep -A3 'livenessProbe:' <<<"$api_deployment" | grep -q '/healthz' || fail "livenessProbe must be /healthz"
grep -A3 'readinessProbe:' <<<"$api_deployment" | grep -q '/readyz' || fail "readinessProbe must be /readyz"
grep -q 'image: "warehouse/checkout:v0.1.0"' <<<"$api_deployment" \
  || fail "an empty image.tag must resolve to v<appVersion>, the tag the release job publishes"
grace="$(awk '/terminationGracePeriodSeconds:/ {print $2; exit}' <<<"$api_deployment")"
# SHUTDOWN_DRAIN_DELAY 5s + HTTP 10s + 5 schedulers x 10s + relay 10s = 75s.
(( grace > 75 )) || fail "terminationGracePeriodSeconds is $grace; it must exceed the binary's shutdown bounds (75s)"

# No *_MODE or in-memory/permissive wording anywhere in the render.
render_all="$(helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}")"
if grep -qE '(^|[^A-Z_])[A-Z]+_MODE\b' <<<"$render_all"; then fail "the render carries a *_MODE variable, which this service does not have"; fi
if grep -qiE 'in-memory|silently runs|permissive' <<<"$render_all"$'\n'"$(cat "$chart_dir/templates/NOTES.txt")"; then
  fail "the render or NOTES.txt mentions an in-memory / permissive mode"
fi
# This context consumes no topic: no consumer group anywhere.
if grep -qE '[A-Z_]*CONSUMER_GROUP' <<<"$render_all"; then fail "checkout consumes no topic: the render must carry no *_CONSUMER_GROUP"; fi

# Only the api component renders by default: one Deployment, one Service.
for stray in mcp a2a frontend projector reports analytics; do
  if grep -qE "app.kubernetes.io/component: .*$stray" <<<"$(helm template "$release" "$chart_dir" "${base[@]}")"; then
    fail "the default render must not contain a $stray component"
  fi
done
default_render="$(helm template "$release" "$chart_dir" "${base[@]}")"
[[ "$(grep -c '^kind: Deployment' <<<"$default_render")" == 1 ]] || fail "exactly one Deployment (the api) must render by default"
[[ "$(grep -c '^kind: Service$' <<<"$default_render")" == 1 ]] || fail "exactly one Service (the api) must render by default"
for off in HorizontalPodAutoscaler Ingress HTTPRoute; do
  if grep -q "^kind: $off" <<<"$default_render"; then fail "$off must be off by default"; fi
  grep -q "^kind: $off" <<<"$render_all" || fail "$off must render when enabled"
done

# --- refusals: the mandatory values name themselves ---------------------------------
expect_refusal() { # expect_refusal <why> <needle> <helm args...>
  local why="$1" needle="$2" err; shift 2
  if err="$(helm template "$release" "$chart_dir" "$@" 2>&1)"; then
    fail "the chart must refuse to render: $why"
  fi
  grep -qF -- "$needle" <<<"$err" || fail "the refusal for '$why' does not name '$needle':
$err"
}
# args that satisfy everything except the one value a case removes.
mandatory_except() { # mandatory_except <database|kafka|<sibling key>>
  local skip="$1" out=()
  [[ "$skip" == database ]] || out+=(--set database.url=postgres://u@h/db)
  [[ "$skip" == kafka ]] || out+=(--set kafka.brokers=k:9092)
  for s in "${siblings[@]}"; do
    [[ "${s%%:*}" == "$skip" ]] || out+=(--set "config.${s%%:*}=${s#*:}")
  done
  printf '%s\n' "${out[@]}"
}
refusal_without() { # refusal_without <value to leave out> <needle>
  local args=()
  while IFS= read -r line; do args+=("$line"); done < <(mandatory_except "$1")
  expect_refusal "no $1" "$2" "${args[@]}"
}
refusal_without database "database.url or database.existingSecret"
refusal_without kafka "kafka.brokers"
for s in "${siblings[@]}"; do refusal_without "${s%%:*}" "config.${s%%:*}"; done
expect_refusal "a *_MODE through extraEnv" "_MODE" "${base[@]}" --set 'extraEnv[0].name=CHECKOUT_MODE' --set 'extraEnv[0].value=permissive'
expect_refusal "a *_MODE through extraEnv (off)" "_MODE" "${base[@]}" --set 'extraEnv[0].name=SAGA_MODE' --set 'extraEnv[0].value=off'
expect_refusal "a consumer group through extraEnv" "_CONSUMER_GROUP" "${base[@]}" --set 'extraEnv[0].name=PAYMENTS_CONSUMER_GROUP' --set 'extraEnv[0].value=x'
expect_refusal "a chart-owned variable through extraEnv" "DATABASE_URL" "${base[@]}" --set 'extraEnv[0].name=DATABASE_URL' --set 'extraEnv[0].value=x'
expect_refusal "a grace period under the shutdown bounds" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=65
expect_refusal "a grace period equal to the shutdown bounds" "must exceed 75" "${base[@]}" --set terminationGracePeriodSeconds=75
expect_refusal "no grace period" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=0
helm template "$release" "$chart_dir" "${base[@]}" --set terminationGracePeriodSeconds=76 >/dev/null \
  || fail "a 76s grace period (just above the 75s bounds) must still render"
# A longer drain delay raises the bar by the same amount.
expect_refusal "a 30s drain delay with the default grace period" "must exceed 100" "${base[@]}" --set config.shutdownDrainDelay=30s
helm template "$release" "$chart_dir" "${base[@]}" --set config.shutdownDrainDelay=30s --set terminationGracePeriodSeconds=101 >/dev/null \
  || fail "a 30s drain delay with a 101s grace period must render"
expect_refusal "a drain delay the chart cannot size" "config.shutdownDrainDelay" "${base[@]}" --set config.shutdownDrainDelay=soon
expect_refusal "an http address off the container port" "config.httpAddr" "${base[@]}" --set config.httpAddr=:9090

# --- the optional component (ADR 0005 mcp) ------------------------------------
# It is OFF by default: the default render stays exactly one Deployment and one
# Service (asserted above). Here it renders alone and together with the api.

# binary_env_of <files...>: the env literals one binary reads.
binary_env_of() {
  grep -hoE '(Getenv|getenv|env)\("[A-Z][A-Z0-9_]*"' "$@" 2>/dev/null | sed -E 's/.*"([A-Z0-9_]+)".*/\1/' | sort -u
}
# assert_env_rendered <rendered> <label> <vars...>
assert_env_rendered() {
  local rendered="$1" label="$2"; shift 2
  local var
  for var in "$@"; do
    grep -q "name: $var\$" <<<"$rendered" || fail "$label does not render $var, which its binary reads"
  done
}

# mcp: its own env, never the api ConfigMap, never Kafka, never a sibling URL
# (read-only, ADR 0005).
mcp_dep="$(helm template "$release" "$chart_dir" "${base[@]}" --set mcp.enabled=true --show-only templates/mcp-deployment.yaml)"
grep -q 'app.kubernetes.io/component: mcp' <<<"$mcp_dep" || fail "the mcp Deployment was not rendered with mcp.enabled=true"
grep -q 'command: \["/app/mcp"\]' <<<"$mcp_dep" || fail "the mcp Deployment must run /app/mcp"
if grep -q 'configMapRef:' <<<"$mcp_dep"; then fail "mcp-deployment.yaml must not mount the api ConfigMap"; fi
if grep -Eq 'name: (KAFKA_BROKERS|SHOPPING_CART_URL|DELIVERY_PROMISE_URL|PRICING_URL|CUSTOMER_PAYMENTS_URL|CUSTOMER_ORDERING_URL|SIBLING_TIMEOUT|CUSTOMER_ORDERING_TIMEOUT|[A-Z]+_SWEEP_INTERVAL|OUTBOX_[A-Z_]+|SHUTDOWN_DRAIN_DELAY|[A-Z_]*CONSUMER_GROUP)$' <<<"$mcp_dep"; then
  fail "the mcp Deployment must not get Kafka/relay/sibling/scheduler env (read-only, never dials Kafka, calls no sibling)"
fi
# shellcheck disable=SC2207
mcp_vars=($(binary_env_of "$repo_dir"/cmd/mcp/main.go "$repo_dir"/cmd/mcp/router.go))
(( ${#mcp_vars[@]} >= 4 )) || fail "found only ${#mcp_vars[@]} env literals in cmd/mcp; the scan is looking in the wrong place"
assert_env_rendered "$mcp_dep" "the mcp Deployment" "${mcp_vars[@]}" DATABASE_URL MIGRATIONS_DATABASE_URL MCP_ADDR LOG_LEVEL
grep -A4 'name: DATABASE_URL$' <<<"$mcp_dep" | grep -q 'secretKeyRef:' || fail "the mcp DATABASE_URL must come from the same Secret"
mcp_svc="$(helm template "$release" "$chart_dir" "${base[@]}" --set mcp.enabled=true --show-only templates/mcp-service.yaml)"
grep -q 'kind: Service' <<<"$mcp_svc" || fail "the mcp Service was not rendered with mcp.enabled=true"
grep -q 'app.kubernetes.io/component: mcp' <<<"$mcp_svc" || fail "the mcp Service must select component=mcp"

# The mcp route is a SEPARATE switch: mcp on alone exposes nothing.
mcp_noroute="$(helm template "$release" "$chart_dir" "${base[@]}" --set mcp.enabled=true)"
if grep -q 'kind: HTTPRoute' <<<"$mcp_noroute"; then fail "the mcp HTTPRoute must not render while mcp.route.enabled is false"; fi
mcp_route="$(helm template "$release" "$chart_dir" "${base[@]}" --set mcp.enabled=true --set mcp.route.enabled=true --show-only templates/mcp-route.yaml)"
grep -q 'value: /api/mcp/checkout' <<<"$mcp_route" || fail "the mcp route must carry its configured host path"
grep -q 'replacePrefixMatch: /mcp' <<<"$mcp_route" || fail "the mcp route must rewrite the prefix to /mcp (the SDK's canonical endpoint)"
grep -q "name: $release-mcp" <<<"$mcp_route" || fail "the mcp route must target the mcp Service"

# --- the optional component (ADR 0007 a2a) ------------------------------------
# Read-only like mcp: own env, never the api ConfigMap, never Kafka or a
# publisher, never a sibling URL. Off by default (asserted with the stray loop).
a2a_dep="$(helm template "$release" "$chart_dir" "${base[@]}" --set a2a.enabled=true --show-only templates/a2a-deployment.yaml)"
grep -q 'app.kubernetes.io/component: a2a' <<<"$a2a_dep" || fail "the a2a Deployment was not rendered with a2a.enabled=true"
grep -q 'command: \["/app/a2a"\]' <<<"$a2a_dep" || fail "the a2a Deployment must run /app/a2a"
if grep -q 'configMapRef:' <<<"$a2a_dep"; then fail "a2a-deployment.yaml must not mount the api ConfigMap"; fi
if grep -Eq 'name: (KAFKA_BROKERS|EVENT_PUBLISHER|SHOPPING_CART_URL|DELIVERY_PROMISE_URL|PRICING_URL|CUSTOMER_PAYMENTS_URL|CUSTOMER_ORDERING_URL|SIBLING_TIMEOUT|CUSTOMER_ORDERING_TIMEOUT|[A-Z]+_SWEEP_INTERVAL|OUTBOX_[A-Z_]+|SHUTDOWN_DRAIN_DELAY|[A-Z_]*CONSUMER_GROUP)$' <<<"$a2a_dep"; then
  fail "the a2a Deployment must not get Kafka/publisher/relay/sibling/scheduler env (read-only, never dials Kafka, calls no sibling)"
fi
# Every mandatory variable the binary names ("<VAR> is required") must render.
# The regex allows digits: A2A_AGENT_URL must not be read as A_AGENT_URL.
a2a_sources=()
for f in "$repo_dir"/cmd/a2a/*.go; do [[ "$f" == *_test.go ]] || a2a_sources+=("$f"); done
# shellcheck disable=SC2207
a2a_required=($(grep -hoE '[A-Z][A-Z0-9_]+ is required' "${a2a_sources[@]}" | sed -E 's/ is required//' | sort -u || true))
(( ${#a2a_required[@]} >= 2 )) || fail "found only ${#a2a_required[@]} mandatory env names in cmd/a2a; the scan is looking in the wrong place"
for v in A2A_AGENT_URL DATABASE_URL; do
  printf '%s\n' "${a2a_required[@]}" | grep -qx "$v" || fail "cmd/a2a no longer names $v as '<VAR> is required'; the chart scan would drop it"
done
# shellcheck disable=SC2207
a2a_vars=($(binary_env_of "$repo_dir"/cmd/a2a/main.go "$repo_dir"/cmd/a2a/router.go "$repo_dir"/cmd/a2a/telemetry.go))
(( ${#a2a_vars[@]} >= 4 )) || fail "found only ${#a2a_vars[@]} env literals in cmd/a2a; the scan is looking in the wrong place"
assert_env_rendered "$a2a_dep" "the a2a Deployment" "${a2a_required[@]}" "${a2a_vars[@]}" DATABASE_URL MIGRATIONS_DATABASE_URL A2A_ADDR A2A_AGENT_URL LOG_LEVEL
grep -A4 'name: DATABASE_URL$' <<<"$a2a_dep" | grep -q 'secretKeyRef:' || fail "the a2a DATABASE_URL must come from the same Secret"
# A2A_AGENT_URL defaults to the in-cluster Service DNS (namespace-derived, so
# pattern-matched) and is overridable through a2a.agentUrl.
a2a_url="$(grep -A1 'name: A2A_AGENT_URL$' <<<"$a2a_dep" | grep 'value:' || true)"
[[ "$a2a_url" =~ value:\ \"http://$release-a2a\.[a-z0-9-]+\.svc\.cluster\.local:8093/\"$ ]] \
  || fail "A2A_AGENT_URL must default to http://$release-a2a.<ns>.svc.cluster.local:8093/, got: ${a2a_url:-<missing>}"
a2a_dep_url="$(helm template "$release" "$chart_dir" "${base[@]}" --set a2a.enabled=true --set a2a.agentUrl=https://gw.example.invalid/api/a2a/checkout/ --show-only templates/a2a-deployment.yaml)"
grep -A1 'name: A2A_AGENT_URL$' <<<"$a2a_dep_url" | grep -q 'value: "https://gw.example.invalid/api/a2a/checkout/"' || fail "a2a.agentUrl must override A2A_AGENT_URL"
a2a_svc="$(helm template "$release" "$chart_dir" "${base[@]}" --set a2a.enabled=true --show-only templates/a2a-service.yaml)"
grep -q 'kind: Service' <<<"$a2a_svc" || fail "the a2a Service was not rendered with a2a.enabled=true"
grep -q 'app.kubernetes.io/component: a2a' <<<"$a2a_svc" || fail "the a2a Service must select component=a2a"
grep -q 'port: 8093' <<<"$a2a_svc" || fail "the a2a Service must listen on 8093"

# The a2a route is a SEPARATE switch: a2a on alone exposes nothing.
a2a_noroute="$(helm template "$release" "$chart_dir" "${base[@]}" --set a2a.enabled=true)"
if grep -q 'kind: HTTPRoute' <<<"$a2a_noroute"; then fail "the a2a HTTPRoute must not render while a2a.route.enabled is false"; fi
a2a_route="$(helm template "$release" "$chart_dir" "${base[@]}" --set a2a.enabled=true --set a2a.route.enabled=true --show-only templates/a2a-route.yaml)"
grep -q 'value: /api/a2a/checkout' <<<"$a2a_route" || fail "the a2a route must carry its configured host path"
grep -q 'replacePrefixMatch: /$' <<<"$a2a_route" || fail "the a2a route must rewrite the prefix to / (the JSON-RPC endpoint)"
grep -q "name: $release-a2a" <<<"$a2a_route" || fail "the a2a route must target the a2a Service"
# ALL components render TOGETHER (ct lint's values file).
together="$(helm template "$release" "$chart_dir" -f "$chart_dir/ci/all-components-values.yaml")"
for component in api mcp a2a; do
  grep -q "app.kubernetes.io/component: $component\$" <<<"$together" || fail "the full render lacks the $component component"
done
[[ "$(grep -c '^kind: Deployment' <<<"$together")" == 3 ]] || fail "the full render must carry the api, mcp and a2a Deployments"
[[ "$(grep -c '^kind: Service$' <<<"$together")" == 3 ]] || fail "the full render must carry the api, mcp and a2a Services"

echo "PASS: checkout chart render assertions"
