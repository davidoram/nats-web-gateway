# Gateway configuration

The `nats_web_gateway` Caddy HTTP handler exposes only operator-declared
operations. Configuration is validated before traffic is served; empty,
incomplete, ambiguous, and unsafe route sets fail closed. Unmatched requests
pass to the next Caddy handler.

This is the complete reference for the implemented JSON and Caddyfile surfaces.
Durations use Caddy strings such as `250ms`, `2s`, and `15m`; byte counts are
decimal integers. Unless stated otherwise, there is no default: required values
must be set inline or inherited from a profile.

## Complete examples

### JSON

`handler` is Caddy's module discriminator, not a gateway struct field.

```json
{
  "handler": "nats_web_gateway",
  "nats": {
    "urls": ["nats://127.0.0.1:4222"],
    "username": "gateway",
    "password": "{env.NATS_GATEWAY_PASSWORD}",
    "connect_timeout": "5s",
    "reconnect_wait": "1s",
    "max_reconnects": -1,
    "drain_timeout": "30s"
  },
  "route_profiles": [{
    "name": "json_api",
    "request_headers": ["Content-Type"],
    "timeout": "2s",
    "max_request_body_bytes": 1048576,
    "max_reply_bytes": 1048576,
    "response": {
      "mode": "json",
      "headers": ["ETag"],
      "content_type": "application/json",
      "representations": ["application/vnd.example.order+json"],
      "negotiate_accept": true,
      "service_error_statuses": {"4001": 400}
    },
    "stream_mode": "request_reply"
  }],
  "routes": [{
    "name": "get_order",
    "path": "/orders/{id}",
    "methods": ["GET"],
    "profile": "json_api",
    "subject": "orders.{id}",
    "parameters": {
      "id": {"source": "path", "name": "id", "pattern": "^[A-Za-z0-9_-]+$"}
    },
    "extend": {"request_headers": ["Traceparent"]}
  }]
}
```

### Caddyfile

```caddyfile
nats_web_gateway {
  nats_urls nats://127.0.0.1:4222
  nats_user gateway
  nats_password {env.NATS_GATEWAY_PASSWORD}
  connect_timeout 5s
  reconnect_wait 1s
  max_reconnects -1
  drain_timeout 30s

  route_profile json_api {
    request_headers Content-Type
    timeout 2s
    max_request_body_bytes 1048576
    max_reply_bytes 1048576
    response_mode json
    response_headers ETag
    response_content_type application/json
    response_representations application/vnd.example.order+json
    negotiate_accept true
    service_error_status 4001 400
    stream_mode request_reply
  }
  route get_order {
    use json_api
    path /orders/{id}
    methods GET
    subject orders.{id}
    parameter id path id ^[A-Za-z0-9_-]+$
    extend_request_headers Traceparent
  }
}
```

Caddyfile adaptation emits fully resolved routes and removes profiles. JSON is
resolved during validation and provisioning, so adapted JSON exposes effective
policy without serializing runtime credentials.

### `handler` / `nats_web_gateway`

JSON: required Caddy module discriminator `"handler":"nats_web_gateway"` at
the HTTP handler-module level. Caddyfile: the enclosing
`nats_web_gateway { ... }` directive, with no arguments. There is no default.
Caddy uses it to select this module; it is not inheritable and has no per-request
runtime value. Minimal Caddyfile example:

```caddyfile
nats_web_gateway {
  # NATS settings and at least one route go here.
}
```

## Reusable route policy

Profiles may contain `subject`, `parameters`, `request_headers`, `timeout`,
`max_request_body_bytes`, `max_reply_bytes`, `response`, `stream_mode`,
`core_sse`, and `security_context`, including all nested fields. Route identity
and matching fields `name`, `path`, and `methods` are never shareable.

Omission inherits. A child profile or route overrides a set field; nested
objects merge field by field. Lists and maps replace inherited collections,
including with explicit empty JSON collections. `clear` removes a field before
overrides; `extend` adds entries to supported collections. Invalid effective
routes, duplicate names or extensions, unknown references, and cycles fail
closed. Reloads create new profiles, pools, and quotas; a bad new reference
leaves the prior Caddy configuration active.

### `extend`

JSON: optional object `extend` on a route or profile. There is no Caddyfile
`extend` object; its five operations are the flattened directives documented
below. Default: absent. Extensions run after clearing and replacement, add
rather than replace entries, and fail if they create duplicate or invalid
effective policy. Example: `{"extend":{"request_headers":["Traceparent"]}}`.

### `route_profiles`

JSON: optional array `route_profiles`. Caddyfile: repeat
`route_profile <name> { ... }`. Default: none. Each item is reusable route policy
and must have a unique name. Example:

```json
{"route_profiles":[{"name":"bounded","timeout":"2s"}]}
```

### Profile `name`

JSON: required string `route_profiles[].name`. Caddyfile: positional `<name>` in
`route_profile <name>`. It must start with a Unicode letter and then contain only
letters, digits, `_`, or `-`. It is reference metadata, not request data.
Example: `route_profile bounded { timeout 2s }`.

### Profile `extends`

JSON: optional string `route_profiles[].extends`. Caddyfile:
`extends <profile>` inside `route_profile`; unavailable in `route`. Default:
none. It names exactly one existing parent. The parent resolves first; unknown
parents and cycles fail. Example:

```json
{"name":"large","extends":"bounded","max_reply_bytes":1048576}
```

### Route `profile` / Caddyfile `use`

JSON: optional string `routes[].profile`. Caddyfile: `use <profile>` inside
`route`. Default: none. It names one existing profile; profile policy resolves
before route overrides. Example: `use json_api`.

### `clear`

JSON: optional string array `clear` on a route or profile. Caddyfile:
`clear <field>...`. Default: none. Entries must be unique shareable field names.
Route-only `name`, `path`, `methods`, and `profile`, plus profile-only `extends`,
cannot be cleared. A required field must be restored by a later override or the
effective route fails. Clearing `security_context` switches to the operator
connection. Example:

```json
{"profile":"negotiated","clear":["response.representations","response.negotiate_accept"]}
```

The exact clearable names are:

```text
subject parameters request_headers timeout max_request_body_bytes max_reply_bytes
response response.mode response.headers response.content_type
response.representations response.negotiate_accept response.service_error_statuses
stream_mode core_sse core_sse.buffer_messages core_sse.buffer_bytes
core_sse.heartbeat_interval core_sse.max_duration core_sse.max_connections
security_context security_context.mechanism security_context.max_credential_bytes
security_context.max_connections security_context.idle_timeout
security_context.max_lifetime security_context.downstream_identity
security_context.downstream_identity.source security_context.downstream_identity.header
security_context.downstream_identity.max_value_bytes
```

### `extend.parameters` / `extend_parameter`

JSON: optional map `extend.parameters`. Caddyfile: repeat
`extend_parameter <template> <path|query> <HTTP-name> <anchored-regexp>`.
Default: none. Adds parameters after replacement; inherited or sibling keys
cannot be duplicated. Normal parameter validation applies. Example:
`extend_parameter view query view ^[A-Za-z0-9_-]+$`.

### `extend.request_headers` / `extend_request_headers`

JSON: optional string array `extend.request_headers`. Caddyfile:
`extend_request_headers <Header>...`. Default: none. Appends to the inherited
request allowlist; duplicates and forbidden headers fail. Example:
`extend_request_headers Traceparent`.

### `extend.response_headers` / `extend_response_headers`

JSON: optional string array `extend.response_headers`. Caddyfile:
`extend_response_headers <Header>...`. Default: none. Appends to
`response.headers`; normal header safety rules apply. Example:
`extend_response_headers ETag`.

### `extend.representations` / `extend_response_representations`

JSON: optional string array `extend.representations`. Caddyfile:
`extend_response_representations <media-type>...`. Default: none. Appends media
types; the effective route must enable negotiation and remain unique and
mode-compatible. Example:
`extend_response_representations application/vnd.example.v2+json`.

### `extend.service_error_statuses` / `extend_service_error_status`

JSON: optional object `extend.service_error_statuses`. Caddyfile: repeat
`extend_service_error_status <code> <status>`. Default: none. Adds mappings but
cannot replace an existing code; use normal map replacement for that. Example:
`extend_service_error_status 4041 404`.

## Top-level NATS settings

JSON requires the `nats` object. Caddyfile fields are direct children of
`nats_web_gateway`. An operator connection is opened only when an effective
route lacks `security_context`; all-protected configurations authenticate
lazily through route pools.

### `nats`

JSON: required object `nats`. There is no Caddyfile `nats` block; its fields are
flattened directly inside `nats_web_gateway`. No default. The nested connection
policy must validate even when every route is protected. Example:

```json
{"nats":{"urls":["nats://127.0.0.1:4222"],"connect_timeout":"5s","reconnect_wait":"1s","max_reconnects":-1,"drain_timeout":"30s"}}
```

### `nats.urls` / `nats_urls`

JSON: required non-empty string array `nats.urls`. Caddyfile:
`nats_urls <URL>...`. No default. Each absolute URL must have a host, use `nats`,
`tls`, `ws`, or `wss`, and contain no credentials. Example:
`nats_urls nats://127.0.0.1:4222`.

### `nats.username` / `nats_user`

JSON: optional string `nats.username`. Caddyfile: `nats_user <username>`.
Default: empty. It must be set together with `password`, authenticates only the
least-privilege operator connection, and is never fallback for protected routes.
Example: `nats_user gateway`.

### `nats.password` / `nats_password`

JSON: optional string `nats.password`. Caddyfile: `nats_password <placeholder>`.
Default: empty. It must accompany `username` and be exactly one Caddy environment
placeholder `{env.NAME}`. Literal or URL-embedded credentials are rejected; the
secret must never be logged. Example:
`nats_password {env.NATS_GATEWAY_PASSWORD}`.

### `nats.connect_timeout` / `connect_timeout`

JSON: required positive duration `nats.connect_timeout`. Caddyfile:
`connect_timeout <duration>`. No valid zero default. It bounds initial operator
connection establishment; failure rejects provisioning when that connection is
needed. Example: `connect_timeout 5s`.

### `nats.reconnect_wait` / `reconnect_wait`

JSON: required positive duration `nats.reconnect_wait`. Caddyfile:
`reconnect_wait <duration>`. No valid zero default. It delays operator reconnect
attempts. The handler is unready while disconnected and ready after reconnect.
Example: `reconnect_wait 1s`.

### `nats.max_reconnects` / `max_reconnects`

JSON: integer `nats.max_reconnects`. Caddyfile: `max_reconnects <integer>`.
Default: `0`. Valid values are `-1` (retry for the handler lifetime) or any
non-negative bound; values below `-1` fail. Example: `max_reconnects -1`.

### `nats.drain_timeout` / `drain_timeout`

JSON: required positive duration `nats.drain_timeout`. Caddyfile:
`drain_timeout <duration>`. No valid zero default. Cleanup becomes unready,
drains subscriptions and buffered publishes for at most this duration, then
forces closure. Reload never retries application requests. Example:
`drain_timeout 30s`.

## Route matching and subject construction

JSON requires non-empty `routes`; Caddyfile repeats `route <name> { ... }`.
Effective route names must be unique. Overlapping path shapes with overlapping
method sets are rejected.

### `routes`

JSON: required non-empty array `routes`. Caddyfile: repeat
`route <name> { ... }`; there is no enclosing `routes` directive. No default.
Every entry must resolve to complete valid policy. Example:

```json
{"routes":[{"name":"health","path":"/health","methods":["GET"],"subject":"health","timeout":"1s","max_request_body_bytes":1,"max_reply_bytes":1024,"response":{"mode":"json","content_type":"application/json"},"stream_mode":"request_reply"}]}
```

### Route `name`

JSON: required string `routes[].name`. Caddyfile: positional name in
`route <name>`. No default and unavailable in profiles. It follows the profile
name grammar and identifies policy, not caller input. Example:
`route get_order { ... }`.

### `path`

JSON: required string `path`. Caddyfile: `path <absolute-template>`. No default
and unavailable in profiles. It must be absolute, without query, fragment,
wildcard, whitespace, backslash, empty segment, or trailing slash (except `/`).
Placeholders occupy complete segments. Nonmatches fall through. Example:
`path /orders/{id}`.

### `methods`

JSON: required non-empty string array `methods`. Caddyfile:
`methods <METHOD>...`. No default and unavailable in profiles. Values are unique
uppercase valid HTTP tokens. Nonmatches fall through. Example: `methods GET`.

### `subject`

JSON: required string `subject`. Caddyfile: `subject <template>`. No default;
shareable and clearable. It has non-empty dot-separated tokens, no wildcard or
whitespace, and placeholders only as complete tokens. Callers can supply only
validated parameters, never arbitrary subjects. Example: `subject orders.{id}`.

### `parameters`

JSON: optional map `parameters` from template names to parameter objects.
Caddyfile: repeat
`parameter <template> <source> <HTTP-name> <pattern>`. Default: empty;
shareable, replaceable with `{}`, clearable, and extendable. Every placeholder
needs exactly one entry and unused entries fail. Example:

```json
{"parameters":{"id":{"source":"path","name":"id","pattern":"^[A-Za-z0-9_-]+$"}}}
```

### Parameter `source`

JSON: required string `parameters.<template>.source`. Caddyfile: second
`parameter` argument. Values: `path` or `query`; no default. Path placeholders
must use `path`; query is for subject-only placeholders. Example:
`"source":"path"`.

### Parameter `name`

JSON: required string `parameters.<template>.name`. Caddyfile: third argument.
No default. A path name equals its placeholder. A query name starts with an
ASCII letter and continues with letters, digits, `_`, `.`, or `-`. Example:
`parameter term query q ^[A-Za-z0-9_-]+$`.

### Parameter `pattern`

JSON: required string `parameters.<template>.pattern`. Caddyfile: fourth
argument. No default. It is a valid explicitly `^`/`$` anchored Go regexp,
matches at least one character, and permits only ASCII letters, digits, `_`, or
`-`; unsafe operations fail. The entire value must match or HTTP returns `400`.
Example: `"pattern":"^[A-Za-z0-9_-]+$"`.

## Request policy

### `request_headers`

JSON: optional string array `request_headers`. Caddyfile:
`request_headers <Header>...`. Default: empty allowlist. It is shareable,
replaceable, clearable, and extendable. Canonical unique names are required;
credential, cookie, hop-by-hop, NATS, and identity-reserved headers are
forbidden. Only listed headers reach NATS. Example:
`request_headers Content-Type Traceparent`.

### `timeout`

JSON: required positive duration `timeout`. Caddyfile: `timeout <duration>`.
No valid zero default; shareable and clearable. It bounds protected connection
acquisition and NATS request/reply, or Core SSE subscription setup. HTTP
cancellation propagates. Expiry returns `504`; publication is never retried and
may already have occurred. Example: `timeout 2s`.

### `max_request_body_bytes`

JSON: required positive integer `max_request_body_bytes`. Caddyfile:
`max_request_body_bytes <bytes>`. No default; shareable and clearable. It is a
hard request bound; excess input returns `413` without publish. Core SSE still
requires an explicit bound. Example: `max_request_body_bytes 1048576`.

### `max_reply_bytes`

JSON: required positive integer `max_reply_bytes`. Caddyfile:
`max_reply_bytes <bytes>`. No default; shareable and clearable. It bounds a
reply or each SSE event. Oversized replies return `502`; oversized events close
that stream with a bounded error event. Example: `max_reply_bytes 1048576`.

## Response policy

Every effective route requires JSON object `response`. Caddyfile response fields
are flattened. Nested fields inherit independently unless cleared or replaced.

### `response`

JSON: required object `response`. There is no Caddyfile `response` object; its
fields use the flattened directives below. No default. It is shareable and
clearable, and otherwise merges field by field. Clearing it requires restoring
`mode` and `content_type`. Example:
`{"response":{"mode":"json","content_type":"application/json"}}`.

### `response.mode` / `response_mode`

JSON: required string `response.mode`. Caddyfile: `response_mode <mode>`. No
default; shareable and clearable. Values: `json` or `binary`. JSON requires one
valid JSON value or returns `502`; binary remains opaque and bounded. Example:
`response_mode json`.

### `response.headers` / `response_headers`

JSON: optional string array `response.headers`. Caddyfile:
`response_headers <Header>...`. Default: empty allowlist. It is shareable,
replaceable, clearable, and extendable. Canonical unique names are required;
credential, cookie, hop-by-hop, and NATS headers are forbidden. Only listed
reply headers reach HTTP. Example: `response_headers ETag`.

### `response.content_type` / `response_content_type`

JSON: required string `response.content_type`. Caddyfile:
`response_content_type <media-type>`. No default; shareable and clearable. It is
a canonical parameter-free media type without line breaks. JSON mode permits
`application/json` or `+json`. It becomes HTTP `Content-Type`, with
`X-Content-Type-Options: nosniff`; conflicting upstream type returns `502`.
Example: `response_content_type application/json`.

### `response.representations` / `response_representations`

JSON: optional string array `response.representations`. Caddyfile:
`response_representations <media-type>...`. Default: empty; shareable,
replaceable, clearable, and extendable. It must be non-empty exactly when
negotiation is true. Entries obey content-type rules, are unique including the
primary type, and are JSON types in JSON mode. No transcoding occurs. Example:
`response_representations application/vnd.example.order+json`.

### `response.negotiate_accept` / `negotiate_accept`

JSON: optional boolean `response.negotiate_accept`. Caddyfile:
`negotiate_accept <true|false>`. Default: `false`; shareable and clearable.
When true, representations are required. A bounded valid `Accept` selects only
a declared type, which is sent to NATS; failure returns `406` before publish.
To turn off inherited `true`, use `clear response.negotiate_accept` because a
JSON `false` merges like omission. Example: `negotiate_accept true`.

### `response.service_error_statuses` / `service_error_status`

JSON: optional object `response.service_error_statuses` mapping code strings to
integer statuses. Caddyfile: repeat `service_error_status <code> <status>`.
Default: empty; shareable, replaceable, clearable, and extendable. A code is a
positive canonical decimal of at most ten digits; status is `400`–`599`.
Unmapped/malformed ADR-32 errors return `502`; descriptions are not exposed.
Example: `service_error_status 4041 404`.

## Streaming settings

### `stream_mode`

JSON: required string `stream_mode`. Caddyfile: `stream_mode <mode>`. No
default; shareable and clearable. `request_reply` performs one at-most-once
request. `core_sse` requires `core_sse`. Reserved `jetstream_sse` currently
always fails validation. Example: `stream_mode request_reply`.

### `core_sse`

JSON: object `core_sse`, required only for `core_sse`; Caddyfile has only the
flattened fields below. Default: absent. It is forbidden in other modes,
shareable, clearable, and field-merged. All five fields are required. Core NATS
SSE is ephemeral, best-effort, at-most-once, without persistence, ack, replay,
event IDs, or `Last-Event-ID`. Example:

```json
{"core_sse":{"buffer_messages":32,"buffer_bytes":1048576,"heartbeat_interval":"15s","max_duration":"15m","max_connections":100}}
```

### `core_sse.buffer_messages` / `core_sse_buffer_messages`

JSON: required positive integer `core_sse.buffer_messages`. Caddyfile:
`core_sse_buffer_messages <count>`. No default; shareable and clearable. It
bounds each client's queued message count. Either queue bound ends that stream
as a slow consumer without blocking NATS. Example:
`core_sse_buffer_messages 32`.

### `core_sse.buffer_bytes` / `core_sse_buffer_bytes`

JSON: required positive integer bytes `core_sse.buffer_bytes`. Caddyfile:
`core_sse_buffer_bytes <bytes>`. No default; shareable and clearable. It jointly
bounds per-client queue memory. Example: `core_sse_buffer_bytes 1048576`.

### `core_sse.heartbeat_interval` / `core_sse_heartbeat_interval`

JSON: required positive duration `core_sse.heartbeat_interval`. Caddyfile:
`core_sse_heartbeat_interval <duration>`. No default; shareable and clearable.
It must be less than `max_duration` and emits idle SSE comments. Example:
`core_sse_heartbeat_interval 15s`.

### `core_sse.max_duration` / `core_sse_max_duration`

JSON: required positive duration `core_sse.max_duration`. Caddyfile:
`core_sse_max_duration <duration>`. No default; shareable and clearable. It
closes healthy streams and releases resources. A protected stream closes sooner
at its connection's maximum lifetime. Example: `core_sse_max_duration 15m`.

### `core_sse.max_connections` / `core_sse_max_connections`

JSON: required positive integer `core_sse.max_connections`. Caddyfile:
`core_sse_max_connections <count>`. No default; shareable and clearable. It is
a per-effective-policy, per-handler HTTP stream quota; equivalent profiles share
it, reload instances do not. Excess returns `429` before subscribe. Example:
`core_sse_max_connections 100`.

Invalid UTF-8, oversized events, or buffer exhaustion end only the affected
stream with a bounded error. Cancellation, cleanup, duration, and credential
expiry unsubscribe and release resources.

## Security contexts and credential adapters

Without `security_context`, a route uses the operator connection. With it, one
HTTP credential presentation creates or reuses an isolated NATS connection.
NATS authentication and permissions remain authoritative; no operator fallback
or permission expansion occurs. Equivalent contexts share a handler-owned pool,
but a mechanism-scoped digest of the exact credential isolates entries. Changed
security/lifecycle policy creates a distinct pool; reloads share no pools.

### `security_context`

JSON: optional object `security_context`; Caddyfile has only flattened fields.
Default: absent. It is shareable, clearable, and field-merged. When present,
`mechanism`, `max_connections`, `idle_timeout`, and `max_lifetime` are required.
Credential/authentication failure returns `401`, NATS permission failure `403`,
and pool/connectivity failure `503`. Example:

```json
{"security_context":{"mechanism":"bearer_token","max_connections":100,"idle_timeout":"1m","max_lifetime":"15m"}}
```

### `security_context.mechanism` / `credential_mechanism`

JSON: required string `security_context.mechanism`. Caddyfile:
`credential_mechanism <mechanism>`. No default; shareable and clearable. Values
are `bearer_token`, `user_password`, `nkey`, `nkey_jwt`, and `tls`. Exactly one
presentation is accepted; ambiguity fails closed. The adapter presents proof;
NATS authenticates it. Example: `credential_mechanism bearer_token`.

Presentations are: one Bearer `Authorization` value; HTTP Basic; or trusted
in-process NKey signing proof, NKey JWT/signing callbacks, or TLS client
certificate/private-key proof attached to the request context. Standard Caddy
HTTP configuration does not create the latter three, so they are conditionally
available only through an upstream integration. Mixed proofs fail as ambiguous.
Refreshed NKey JWT identity retires its old pool entry.

### `security_context.max_credential_bytes` / `max_credential_bytes`

JSON: optional non-negative integer bytes
`security_context.max_credential_bytes`. Caddyfile:
`max_credential_bytes <bytes>`. Default: `8192` when absent or zero. Negative
effective values fail. It bounds each textual credential before connection;
oversize returns `401` without logging the value. Shareable and clearable;
clearing restores the default. Example: `max_credential_bytes 4096`.

### `security_context.max_connections` / `max_security_context_connections`

JSON: required positive integer `security_context.max_connections`. Caddyfile:
`max_security_context_connections <count>`. No default; shareable and clearable.
It bounds pooled plus in-flight credential connections per effective policy.
Idle entries may be evicted; otherwise new contexts return `503`. Aliased routes
cannot bypass the shared bound. Example: `max_security_context_connections 100`.

### `security_context.idle_timeout` / `security_context_idle_timeout`

JSON: required positive duration `security_context.idle_timeout`. Caddyfile:
`security_context_idle_timeout <duration>`. No default; shareable and clearable.
Unleased connections close after this idle time; active leases are not ended by
idle timeout alone. Example: `security_context_idle_timeout 1m`.

### `security_context.max_lifetime` / `security_context_max_lifetime`

JSON: required positive duration `security_context.max_lifetime`. Caddyfile:
`security_context_max_lifetime <duration>`. No default; shareable and clearable.
Connections retire at this age, reject new leases, then close after active
leases release; streams are capped at remaining life. Keep it within credential
expiry/revocation requirements. Example: `security_context_max_lifetime 15m`.

## Downstream identity

This optional feature is valid only on protected `request_reply` routes. It asks
NATS for the user authenticated on that connection, strips the configured
caller header, and sends one gateway-generated value. It never trusts caller
headers, credentials, or unauthenticated claims. The NATS user needs publish to
`$SYS.REQ.USER.INFO` and inbox subscribe permission. Missing, denied, malformed,
injected, or oversized identity fails before application publish. Values never
enter logs, metrics, errors, traces, or generated docs. See
[Downstream identity](downstream-identity.md).

### `security_context.downstream_identity`

JSON: optional object `security_context.downstream_identity`; Caddyfile has only
flattened fields. Default: absent. It is shareable, clearable, and field-merged;
all three nested fields are required when present. It is forbidden on streams,
and its header cannot appear in `request_headers`. Example:

```json
{"downstream_identity":{"source":"nats_user_id","header":"X-Authenticated-User","max_value_bytes":256}}
```

### `downstream_identity.source` / `downstream_identity_source`

JSON: required string `security_context.downstream_identity.source`. Caddyfile:
`downstream_identity_source <source>`. No default; shareable and clearable. Only
`nats_user_id` is supported. Example:
`downstream_identity_source nats_user_id`.

### `downstream_identity.header` / `downstream_identity_header`

JSON: required string `security_context.downstream_identity.header`. Caddyfile:
`downstream_identity_header <Header>`. No default; shareable and clearable. It
must be canonical and cannot be credential, hop-by-hop, or NATS-reserved, or
also be request-allowlisted. Caller values are stripped. Example:
`downstream_identity_header X-Authenticated-User`.

### `downstream_identity.max_value_bytes` / `max_downstream_identity_bytes`

JSON: required positive integer bytes
`security_context.downstream_identity.max_value_bytes`. Caddyfile:
`max_downstream_identity_bytes <bytes>`. No default; shareable and clearable.
It bounds the authenticated value; oversize or invalid header values fail before
publish. Example: `max_downstream_identity_bytes 256`.

## Delivery, failure, and reload semantics

A matched `request_reply` route publishes exactly once and never retries.
Invalid input returns `400`; authentication `401`; permission `403`;
unacceptable representation `406`; oversized request `413`; malformed,
oversized, or unmapped ADR-32 reply `502`; no responders, pool exhaustion, or
connectivity `503`; and deadline expiry `504`. Cancellation aborts the wait
without manufacturing a response. Timeout after publication is an ambiguous
application outcome.

Errors are bounded JSON and never copy subjects, service descriptions,
credentials, identity, or payloads. Each handler owns its connections, pools,
subscriptions, timers, and quotas. Reload instances may overlap but share no
mutable state; cleanup drains for `nats.drain_timeout` and then forces closure.

## Runnable examples

The [orders service](../examples/orders-service/main.go) demonstrates JSON and
media negotiation; the [image service](../examples/images-service/main.go)
demonstrates bounded PNG output. Their complete
[Caddyfile](../examples/Caddyfile) is runnable. The
[Pets service](../examples/pets-service/main.go) and matching
[Caddyfile](../examples/pets-service/Caddyfile) demonstrate REST/RPC ADR-32
services, fixed subjects, anchored IDs, and explicit errors. Example in-memory
state is non-production and resets on restart. ADR-32 discovery subjects remain
control-plane interfaces and are never gateway routes.

Run the canonical integration target:

```bash
go tool mage integration
```

Or use the local environment:

```bash
podman-compose --file compose.yml up --detach
podman-compose --file compose.yml down --volumes --remove-orphans
```

Docker users can substitute `docker compose`. Example calls:

```bash
curl -X POST -H 'Content-Type: application/json' \
  --data '{"id":"order-42","status":"pending"}' \
  http://localhost:8080/api/orders
curl -H 'Accept: application/vnd.example.order+json' \
  http://localhost:8080/api/order/order-42
curl -H 'Accept: image/png' \
  http://localhost:8080/assets/logo.png --output logo.png
```

Only allowlisted headers are forwarded. Credentials, cookies, hop-by-hop
headers, and caller identity are never copied. Negotiation adds only the
gateway-selected `Accept` value.
