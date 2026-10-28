{{/*
Expand the name of the chart.
*/}}
{{- define "checkout.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "checkout.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Chart name and version as used by the chart label.
*/}}
{{- define "checkout.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "checkout.labels" -}}
helm.sh/chart: {{ include "checkout.chart" . }}
{{ include "checkout.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels. Every Deployment and Service also pins
app.kubernetes.io/component (api, mcp): a Service selecting on these two alone
would select every component of the release.
*/}}
{{- define "checkout.selectorLabels" -}}
app.kubernetes.io/name: {{ include "checkout.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "checkout.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "checkout.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Fully qualified name of the MCP deployment/service/route (ADR 0005).
*/}}
{{- define "checkout.mcpFullname" -}}
{{- include "checkout.fullname" . }}-mcp
{{- end }}

{{/*
Name of the Secret holding DATABASE_URL: the operator's own
(database.existingSecret) or the one this chart creates from database.url.
*/}}
{{- define "checkout.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- include "checkout.fullname" . }}-database
{{- end }}
{{- end }}

{{/*
Image reference. The release job publishes the image as v<version> (and packages
the chart with appVersion <version>), so an empty image.tag means "v" + appVersion.
*/}}
{{- define "checkout.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default (printf "v%s" .Chart.AppVersion) }}
{{- end }}

{{/*
MANDATORY: both binaries exit at boot without DATABASE_URL (real Postgres only,
no in-memory mode). Fails the render naming the values instead of crash-looping.
*/}}
{{- define "checkout.requireDatabase" -}}
{{- if not (or .Values.database.url .Values.database.existingSecret) -}}
{{- fail "checkout requires database.url or database.existingSecret to be set (DATABASE_URL): the service runs on real Postgres only and exits at boot without it." -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY: cmd/api exits at boot without KAFKA_BROKERS (outbox relay, /readyz).
No default broker and no way to switch Kafka off.
*/}}
{{- define "checkout.requireKafka" -}}
{{- $_ := required "checkout requires kafka.brokers to be set (KAFKA_BROKERS, comma-separated): the service runs on real Kafka only and exits at boot without it." .Values.kafka.brokers -}}
{{- end -}}

{{/*
MANDATORY: the saga calls five siblings synchronously and cmd/api exits at boot
naming the first missing URL (cmd/api/config.go siblingURLs).
*/}}
{{- define "checkout.requireSiblingUrls" -}}
{{- range $s := list (dict "key" "shoppingCartUrl" "env" "SHOPPING_CART_URL") (dict "key" "deliveryPromiseUrl" "env" "DELIVERY_PROMISE_URL") (dict "key" "pricingUrl" "env" "PRICING_URL") (dict "key" "customerPaymentsUrl" "env" "CUSTOMER_PAYMENTS_URL") (dict "key" "customerOrderingUrl" "env" "CUSTOMER_ORDERING_URL") -}}
{{- if not (index $.Values.config $s.key) -}}
{{- fail (printf "config.%s is required (%s): checkout calls this service synchronously and cmd/api exits at boot without it." $s.key $s.env) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
extraEnv may not bring back what the chart owns: no *_MODE variable (this
service has no mode: Postgres and Kafka are the only runtime), no
*_CONSUMER_GROUP (checkout consumes no topic, so there is no group to set), and
none of the variables the chart renders itself (the same variable is never set
twice).
*/}}
{{- define "checkout.requireCleanExtraEnv" -}}
{{- $owned := list "HTTP_ADDR" "LOG_LEVEL" "DATABASE_URL" "MIGRATIONS_DATABASE_URL" "KAFKA_BROKERS" "SHOPPING_CART_URL" "DELIVERY_PROMISE_URL" "PRICING_URL" "CUSTOMER_PAYMENTS_URL" "CUSTOMER_ORDERING_URL" "SIBLING_TIMEOUT" "CUSTOMER_ORDERING_TIMEOUT" "INDOUBT_SWEEP_INTERVAL" "STUCK_SWEEP_INTERVAL" "VOID_SWEEP_INTERVAL" "EXPIRY_SWEEP_INTERVAL" "PURGE_SWEEP_INTERVAL" "OUTBOX_RELAY_INTERVAL" "OUTBOX_MAX_ATTEMPTS" "SHUTDOWN_DRAIN_DELAY" "ENVIRONMENT" "SERVICE_VERSION" "OTEL_SERVICE_NAME" "OTEL_EXPORTER_OTLP_ENDPOINT" -}}
{{- range .Values.extraEnv -}}
{{- $name := toString .name -}}
{{- if hasSuffix "_MODE" $name -}}
{{- fail (printf "extraEnv sets %s: *_MODE variables do not exist here - checkout runs on real Postgres and real Kafka only, with no permissive, off or in-memory mode." $name) -}}
{{- end -}}
{{- if hasSuffix "_CONSUMER_GROUP" $name -}}
{{- fail (printf "extraEnv sets %s: checkout consumes no topic, so there is no consumer group to set (the variable would be dead)." $name) -}}
{{- end -}}
{{- if has $name $owned -}}
{{- fail (printf "extraEnv sets %s, which the chart renders from its own values: use that value instead." $name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
SHUTDOWN_DRAIN_DELAY in whole seconds (the binary accepts Go durations; the chart
accepts "Ns", "Nm" and "0" because it needs the number to size the grace
period). Unset = the binary's default, 5s.
*/}}
{{- define "checkout.drainSeconds" -}}
{{- $raw := toString .Values.config.shutdownDrainDelay -}}
{{- if eq $raw "" -}}5
{{- else if eq $raw "0" -}}0
{{- else if regexMatch "^[0-9]+s$" $raw -}}{{ trimSuffix "s" $raw }}
{{- else if regexMatch "^[0-9]+m$" $raw -}}{{ mul 60 (int (trimSuffix "m" $raw)) }}
{{- else -}}
{{- fail (printf "config.shutdownDrainDelay is %q - want whole seconds (\"5s\"), minutes (\"1m\") or \"0\"." $raw) -}}
{{- end -}}
{{- end -}}

{{/*
The graceful shutdown is sequential (cmd/api/main.go, cmd/api/workers.go):
SHUTDOWN_DRAIN_DELAY, the HTTP drain (10s), then the five schedulers awaited one
after the other (10s each, 50s) and the outbox relay (10s). A grace period that
does not exceed drain + 70s can SIGKILL a scheduler or the relay mid-drain,
stranding an in-flight outbox batch or a half-finished sweep.
*/}}
{{- define "checkout.requireGracePeriod" -}}
{{- $need := add (int (include "checkout.drainSeconds" .)) 70 -}}
{{- if le (int .Values.terminationGracePeriodSeconds) $need -}}
{{- fail (printf "terminationGracePeriodSeconds is %v - it must exceed %v (SHUTDOWN_DRAIN_DELAY %vs + HTTP 10s + 5 schedulers x 10s + relay 10s): a shorter period SIGKILLs a scheduler or the relay mid-drain." .Values.terminationGracePeriodSeconds $need (include "checkout.drainSeconds" .)) -}}
{{- end -}}
{{- end -}}

{{/*
The port of HTTP_ADDR is the container port the probes and the Service target.
*/}}
{{- define "checkout.requireHttpPort" -}}
{{- $port := trimPrefix ":" (regexFind ":[0-9]+$" (toString .Values.config.httpAddr)) -}}
{{- if ne $port (toString .Values.service.targetPort) -}}
{{- fail (printf "config.httpAddr is %q but service.targetPort is %v: the container port, the probes and the Service must agree." .Values.config.httpAddr .Values.service.targetPort) -}}
{{- end -}}
{{- end -}}

{{/*
Every render-time requirement of the real runtime, in one include. The api
Deployment (always rendered) includes it, so a mis-set release fails
`helm template` / `helm install`.
*/}}
{{- define "checkout.requireRealRuntime" -}}
{{- include "checkout.requireDatabase" . -}}
{{- include "checkout.requireKafka" . -}}
{{- include "checkout.requireSiblingUrls" . -}}
{{- include "checkout.requireCleanExtraEnv" . -}}
{{- include "checkout.requireGracePeriod" . -}}
{{- include "checkout.requireHttpPort" . -}}
{{- end -}}
