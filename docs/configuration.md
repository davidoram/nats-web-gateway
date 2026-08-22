# Gateway configuration

The `nats_web_gateway` Caddy HTTP handler exposes only operator-declared
operations. Configuration is validated before traffic is served; empty,
incomplete, ambiguous, and unsafe route sets are rejected. In other words,
Caddy will not start or reload the gateway with a configuration whose behavior
is unclear. Requests that do not match a declared route pass to the next Caddy
handler.

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
    "urls": [
      "nats://127.0.0.1:4222"
    ],
    "username": "gateway",
    "password": "{env.NATS_GATEWAY_PASSWORD}",
    "connect_timeout": "5s",
    "reconnect_wait": "1s",
    "max_reconnects": -1,
    "drain_timeout": "30s"
  },
  "route_profiles": [
    {
      "name": "json_api",
      "request_headers": [
        "Content-Type"
      ],
      "timeout": "2s",
      "max_request_body_bytes": 1048576,
      "max_reply_bytes": 1048576,
      "response": {
        "mode": "json",
        "headers": [
          "ETag"
        ],
        "content_type": "application/json",
        "representations": [
          "application/vnd.example.order+json"
        ],
        "negotiate_accept": true,
        "service_error_statuses": {
          "4001": 400
        }
      },
      "stream_mode": "request_reply"
    }
  ],
  "routes": [
    {
      "name": "get_order",
      "path": "/orders/{id}",
      "methods": [
        "GET"
      ],
      "profile": "json_api",
      "subject": "orders.{id}",
      "parameters": {
        "id": {
          "source": "path",
          "name": "id",
          "pattern": "^[A-Za-z0-9_-]+$"
        }
      },
      "extend": {
        "request_headers": [
          "Traceparent"
        ]
      }
    }
  ]
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

Caddyfile adaptation emits final routes and removes profiles. JSON is resolved
during validation and provisioning, so adapted JSON shows every route's final
settings without displaying runtime credentials.

### `handler` / `nats_web_gateway`

**JSON:** required Caddy module name `"handler":"nats_web_gateway"` at the HTTP
handler level.

**Caddyfile:** the enclosing
`nats_web_gateway { ... }` directive, with no arguments.

**Default:** None.

Caddy uses this value to load the gateway. Profiles cannot change it, and it
does not affect individual requests.

**Example:**

```caddyfile
nats_web_gateway {
  # NATS settings and at least one route go here.
}
```

## Reusable route policy

Profiles save you from repeating the same policy on many routes. For example,
an API may use the same two-second timeout, 1 MiB limits, JSON response rules,
and forwarded headers on every endpoint. Put those settings in one profile,
then keep the URL, HTTP methods, and NATS subject visible on each route:

```caddyfile
route_profile json_api {
  request_headers Content-Type
  timeout 2s
  max_request_body_bytes 1048576
  max_reply_bytes 1048576
  response_mode json
  response_content_type application/json
  stream_mode request_reply
}

route list_orders {
  use json_api
  path /orders
  methods GET
  subject orders.list
}

route create_order {
  use json_api
  path /orders
  methods POST
  subject orders.create
}
```

Both routes receive all the policy from `json_api`. Their `name`, `path`, and
`methods` must still be written on the route, so the exposed HTTP API remains
easy to audit. A profile may share `subject`, `parameters`, request headers,
timeouts, size limits, response policy, streaming policy, and security policy.

There are four ways to build on a profile:

1. **Inherit:** leave a setting out and use the profile's value.
2. **Override:** write a new value on the route or child profile.
3. **Extend:** add entries to an inherited list or map without copying it.
4. **Clear:** remove an inherited value that should not apply.

The gateway resolves these steps before serving traffic. If the result is
incomplete or unsafe, Caddy rejects the configuration. During a reload, a bad
profile change is rejected and the previous working configuration stays active.

### Inheriting and overriding values

Most overrides are straightforward. In this example, file uploads inherit all
of `json_api` but get a 10-second timeout and a larger request limit:

```caddyfile
route upload_order_attachment {
  use json_api
  path /orders/{id}/attachment
  methods POST
  subject orders.attachment.{id}
  parameter id path id ^[A-Za-z0-9_-]+$
  timeout 10s
  max_request_body_bytes 10485760
}
```

Nested objects are handled one setting at a time. Changing
`response_content_type`, for example, does not discard an inherited response
mode or error mapping.

Lists and maps behave differently: writing one replaces the complete inherited
collection. Suppose `json_api` forwards `Content-Type` and `Traceparent`:

```caddyfile
route_profile json_api {
  request_headers Content-Type Traceparent
  # Other shared settings omitted here.
}

route webhook {
  use json_api
  request_headers X-Webhook-Event
  # This route forwards only X-Webhook-Event.
}
```

Replacement is useful when a route needs a deliberately different allowlist.
Use `extend` instead when you want to keep the inherited entries and add one.

### `extend`

**JSON:** Optional object `extend` on a route or profile.

**Caddyfile:** There is no `extend` block. Use the five `extend_*` directives
documented below.

**Default:** No additions.

Use `extend` when most of an inherited list or map is correct and you only need
to add entries. This avoids copying shared policy into the route and means later
profile updates still reach that route.

For example, `create_order` needs the shared `Content-Type` header plus tracing:

```caddyfile
route create_order {
  use json_api
  path /orders
  methods POST
  subject orders.create
  extend_request_headers Traceparent
}
```

The resulting allowlist is `Content-Type, Traceparent`. If the route used
`request_headers Traceparent` instead, it would replace the list and stop
forwarding `Content-Type`.

**Example:** JSON groups additions under the `extend` object:

```json
{
  "profile": "json_api",
  "extend": {
    "request_headers": [
      "Traceparent"
    ],
    "service_error_statuses": {
      "4041": 404
    }
  }
}
```

Extensions are applied after ordinary overrides. Adding a duplicate or unsafe
value rejects the configuration.

### `route_profiles`

**JSON:** optional array `route_profiles`.

**Caddyfile:** repeat
`route_profile <name> { ... }`.

**Default:** none. Each item is reusable route policy
and must have a unique name.

**Example:**

```json
{
  "route_profiles": [
    {
      "name": "bounded",
      "timeout": "2s"
    }
  ]
}
```

### Profile `name`

**JSON:** required string `route_profiles[].name`.

**Caddyfile:** positional `<name>` in
`route_profile <name>`. It must start with a Unicode letter and then contain only
letters, digits, `_`, or `-`. It is reference metadata, not request data.

**Default:** None.

**Example:** `route_profile bounded { timeout 2s }`.

### Profile `extends`

**JSON:** optional string `route_profiles[].extends`.

**Caddyfile:**
`extends <profile>` inside `route_profile`; unavailable in `route`.

**Default:**
none. Use it to make a more specialized profile from an existing one.

**Example:**

For example, most endpoints may use `json_api`, while upload endpoints need
larger limits. Defining `large_upload` once avoids repeating both the base policy
and the upload overrides:

```caddyfile
route_profile large_upload {
  extends json_api
  timeout 10s
  max_request_body_bytes 10485760
}

route upload_order_attachment {
  use large_upload
  path /orders/{id}/attachment
  methods POST
  subject orders.attachment.{id}
  parameter id path id ^[A-Za-z0-9_-]+$
}
```

The parent resolves first, then the child overrides it. A profile may extend
only one parent. Unknown parents and inheritance loops are rejected. JSON uses:

```json
{
  "name": "large_upload",
  "extends": "json_api",
  "timeout": "10s",
  "max_request_body_bytes": 10485760
}
```

### Route `profile` / Caddyfile `use`

**JSON:** optional string `routes[].profile`.

**Caddyfile:** `use <profile>` inside
`route`.

**Default:** none. Use it when a route should start with a named set of
shared policy. The profile is applied first, then route-specific overrides,
clears, and extensions.

**Example:** `use json_api`.

### `clear`

**JSON:** optional string array `clear` on a route or profile.

**Caddyfile:**
`clear <field>...`.

**Default:** none. Use it when a profile enables an optional
feature that one route should not have.

**Example:**

For example, this profile supports both standard JSON and a vendor media type:

```caddyfile
route_profile negotiated_json_api {
  response_mode json
  response_content_type application/json
  response_representations application/vnd.example.order+json
  negotiate_accept true
  # Other required shared settings omitted here.
}
```

An internal health endpoint may always return ordinary JSON. It can remove both
negotiation settings without redefining the rest of the response policy:

```caddyfile
route health {
  use negotiated_json_api
  path /health
  methods GET
  subject health.get
  clear response.representations response.negotiate_accept
}
```

The final health route keeps JSON mode and `application/json`, but no longer
negotiates other representations. The JSON form is:

```json
{
  "profile": "negotiated_json_api",
  "clear": [
    "response.representations",
    "response.negotiate_accept"
  ]
}
```

`clear` happens before values written on the same route, so you can also remove
an inherited value and replace it with new policy. Entries must be unique names
from the shareable-settings list below. Route-only `name`, `path`, `methods`,
and `profile`, plus profile-only `extends`, cannot be cleared. Removing a
required setting without replacing it makes the route invalid. Clearing
`security_context` is especially
significant: it changes the route to use the operator NATS connection, so do it
only when that trust boundary is intentional and least-privilege permissions
are configured.

The exact clearable names are:

- [`subject`](#subject)
- [`parameters`](#parameters)
- [`request_headers`](#request_headers)
- [`timeout`](#timeout)
- [`max_request_body_bytes`](#max_request_body_bytes)
- [`max_reply_bytes`](#max_reply_bytes)
- [`response`](#response)
- [`response.mode`](#responsemode--response_mode)
- [`response.headers`](#responseheaders--response_headers)
- [`response.content_type`](#responsecontent_type--response_content_type)
- [`response.representations`](#responserepresentations--response_representations)
- [`response.negotiate_accept`](#responsenegotiate_accept--negotiate_accept)
- [`response.service_error_statuses`](#responseservice_error_statuses--service_error_status)
- [`stream_mode`](#stream_mode)
- [`core_sse`](#core_sse)
- [`core_sse.buffer_messages`](#core_ssebuffer_messages--core_sse_buffer_messages)
- [`core_sse.buffer_bytes`](#core_ssebuffer_bytes--core_sse_buffer_bytes)
- [`core_sse.heartbeat_interval`](#core_sseheartbeat_interval--core_sse_heartbeat_interval)
- [`core_sse.max_duration`](#core_ssemax_duration--core_sse_max_duration)
- [`core_sse.max_connections`](#core_ssemax_connections--core_sse_max_connections)
- [`security_context`](#security_context)
- [`security_context.mechanism`](#security_contextmechanism--credential_mechanism)
- [`security_context.max_credential_bytes`](#security_contextmax_credential_bytes--max_credential_bytes)
- [`security_context.max_connections`](#security_contextmax_connections--max_security_context_connections)
- [`security_context.idle_timeout`](#security_contextidle_timeout--security_context_idle_timeout)
- [`security_context.max_lifetime`](#security_contextmax_lifetime--security_context_max_lifetime)
- [`security_context.downstream_identity`](#security_contextdownstream_identity)
- [`security_context.downstream_identity.source`](#downstream_identitysource--downstream_identity_source)
- [`security_context.downstream_identity.header`](#downstream_identityheader--downstream_identity_header)
- [`security_context.downstream_identity.max_value_bytes`](#downstream_identitymax_value_bytes--max_downstream_identity_bytes)

### `extend.parameters` / `extend_parameter`

**JSON:** Optional map `extend.parameters`.

**Caddyfile:** Repeat
`extend_parameter <template> <path|query> <HTTP-name> <anchored-regexp>`.

**Default:** No added parameters.

Use this setting when a shared subject template needs one additional validated
value. It adds parameters after replacement; inherited or sibling keys cannot
be duplicated. Normal parameter validation applies.

**Example:** `extend_parameter view query view ^[A-Za-z0-9_-]+$` adds the query
parameter needed by a subject such as `orders.{id}.{view}`.

### `extend.request_headers` / `extend_request_headers`

**JSON:** Optional string array `extend.request_headers`.

**Caddyfile:** `extend_request_headers <Header>...`.

**Default:** No added request headers.

This appends to the inherited request allowlist. Use it, for example, to add
`Traceparent` on one traced route while retaining shared `Content-Type`.
Duplicates and forbidden headers fail.

**Example:** `extend_request_headers Traceparent`.

### `extend.response_headers` / `extend_response_headers`

**JSON:** Optional string array `extend.response_headers`.

**Caddyfile:** `extend_response_headers <Header>...`.

**Default:** No added response headers.

This appends to `response.headers`. Use it when one endpoint returns an extra
safe header, such as `ETag`, without copying the base response allowlist. Normal
header safety rules apply.

**Example:** `extend_response_headers ETag`.

### `extend.representations` / `extend_response_representations`

**JSON:** Optional string array `extend.representations`.

**Caddyfile:** `extend_response_representations <media-type>...`.

**Default:** No added representations.

This appends media types. Use it when one endpoint supports an additional
declared format while keeping the profile's formats. The final route must enable
negotiation, and every representation must remain unique and compatible with
the response mode.

**Example:**
`extend_response_representations application/vnd.example.v2+json`.

### `extend.service_error_statuses` / `extend_service_error_status`

**JSON:** Optional object `extend.service_error_statuses`.

**Caddyfile:** Repeat `extend_service_error_status <code> <status>`.

**Default:** No added service-error mappings.

This adds mappings but cannot replace an existing code; use normal map
replacement for that. Use it when one service endpoint adds an application
error that the common profile does not have.

**Example:** `extend_service_error_status 4041 404` maps that endpoint's “not
found” application code to HTTP 404.

## Top-level NATS settings

JSON requires the `nats` object. Caddyfile fields are direct children of
`nats_web_gateway`. These settings answer four practical questions: where is
NATS, how does the gateway authenticate, how should it reconnect, and how long
may shutdown take?

Routes without a `security_context` share this operator connection. Give its
NATS user only the publish and reply-inbox permissions required by those routes.
If every route has a security context, the gateway does not open this connection;
it connects for each protected user only when that user's first request arrives.

### `nats`

**JSON:** Required object `nats`.

**Caddyfile:** There is no `nats` block. Put its individual directives directly
inside `nats_web_gateway`.

**Default:** None.

The nested connection policy must validate even when every route is protected.

**Example:**

```json
{
  "nats": {
    "urls": [
      "nats://127.0.0.1:4222"
    ],
    "connect_timeout": "5s",
    "reconnect_wait": "1s",
    "max_reconnects": -1,
    "drain_timeout": "30s"
  }
}
```

### `nats.urls` / `nats_urls`

**JSON:** required non-empty string array `nats.urls`.

**Caddyfile:**
`nats_urls <URL>...`.

**Default:** None.

Each absolute URL must have a host, use `nats`, `tls`, `ws`, or `wss`, and
contain no credentials.

**Example:**
`nats_urls nats://127.0.0.1:4222`.

### `nats.username` / `nats_user`

**JSON:** optional string `nats.username`.

**Caddyfile:** `nats_user <username>`.

**Default:** Empty.

It must be set together with `password`, authenticates only the least-privilege
operator connection, and is never fallback for protected routes.

**Example:** `nats_user gateway`.

### `nats.password` / `nats_password`

**JSON:** optional string `nats.password`.

**Caddyfile:** `nats_password <placeholder>`.

**Default:** Empty.

It must accompany `username` and be exactly one Caddy environment placeholder
`{env.NAME}`. Literal or URL-embedded credentials are rejected; the secret must
never be logged.

**Example:**
`nats_password {env.NATS_GATEWAY_PASSWORD}`.

### `nats.connect_timeout` / `connect_timeout`

**JSON:** required positive duration `nats.connect_timeout`.

**Caddyfile:**
`connect_timeout <duration>`.

**Default:** None.

Use it to limit how long startup waits for the operator NATS connection. If that connection is
needed and cannot be established in time, Caddy rejects the configuration.

**Example:** `connect_timeout 5s`.

### `nats.reconnect_wait` / `reconnect_wait`

**JSON:** required positive duration `nats.reconnect_wait`.

**Caddyfile:**
`reconnect_wait <duration>`.

**Default:** None.

It sets the pause between operator reconnect attempts, preventing a tight retry loop. The
handler is unready while disconnected and ready after reconnect.

**Example:**
`reconnect_wait 1s`.

### `nats.max_reconnects` / `max_reconnects`

**JSON:** integer `nats.max_reconnects`.

**Caddyfile:** `max_reconnects <integer>`.

**Default:** `0`.

Valid values are `-1` (retry for the handler lifetime) or any non-negative
bound; values below `-1` fail.

**Example:** `max_reconnects -1`.

### `nats.drain_timeout` / `drain_timeout`

**JSON:** required positive duration `nats.drain_timeout`.

**Caddyfile:**
`drain_timeout <duration>`.

**Default:** None.

On shutdown or reload, the gateway first stops accepting work and gives subscriptions and
buffered publishes this long to finish. It then forces the connection closed.
Reload never retries application requests.

**Example:**
`drain_timeout 30s`.

## Route matching and subject construction

JSON requires non-empty `routes`; Caddyfile repeats `route <name> { ... }`.
Effective route names must be unique. Overlapping path shapes with overlapping
method sets are rejected.

A route is the explicit bridge between one HTTP operation and one NATS subject.
For example, `GET /orders/order-42` can match `/orders/{id}`, validate
`order-42`, and publish to `orders.order-42`. The caller cannot replace that
subject with an arbitrary value.

### `routes`

**JSON:** required non-empty array `routes`.

**Caddyfile:** repeat
`route <name> { ... }`; there is no enclosing `routes` directive.

**Default:** None.

Every entry must resolve to complete valid policy.

**Example:**

```json
{
  "routes": [
    {
      "name": "health",
      "path": "/health",
      "methods": [
        "GET"
      ],
      "subject": "health",
      "timeout": "1s",
      "max_request_body_bytes": 1,
      "max_reply_bytes": 1024,
      "response": {
        "mode": "json",
        "content_type": "application/json"
      },
      "stream_mode": "request_reply"
    }
  ]
}
```

### Route `name`

**JSON:** required string `routes[].name`.

**Caddyfile:** positional name in
`route <name>`.

**Default:** None.

This setting is unavailable in profiles. It follows the profile name grammar
and identifies policy, not caller input.

**Example:**
`route get_order { ... }`.

### `path`

**JSON:** required string `path`.

**Caddyfile:** `path <absolute-template>`.

**Default:** None.

This setting is unavailable in profiles. It must be absolute, without query, fragment,
wildcard, whitespace, backslash, empty segment, or trailing slash (except `/`).
Placeholders occupy complete segments. Nonmatches fall through.

**Example:**
`path /orders/{id}`.

### `methods`

**JSON:** required non-empty string array `methods`.

**Caddyfile:**
`methods <METHOD>...`.

**Default:** None.

This setting is unavailable in profiles. Values are unique uppercase valid HTTP
tokens. Nonmatches fall through.

**Example:** `methods GET`.

### `subject`

**JSON:** required string `subject`.

**Caddyfile:** `subject <template>`.

**Default:** None.

Routes may inherit it from a profile or remove it with `clear`. It has
non-empty dot-separated tokens, no wildcard or whitespace, and placeholders
only as complete tokens. Callers can supply only validated parameters, never
arbitrary subjects.

**Example:** `subject orders.{id}`.

### `parameters`

**JSON:** optional map `parameters` from template names to parameter objects.

**Caddyfile:** repeat
`parameter <template> <source> <HTTP-name> <pattern>`.

**Default:** Empty map.

A route may inherit the map, replace it (including with `{}`), remove it with `clear`,
or add entries with `extend`. Every placeholder needs exactly one entry and
unused entries fail.

**Example:**

```json
{
  "parameters": {
    "id": {
      "source": "path",
      "name": "id",
      "pattern": "^[A-Za-z0-9_-]+$"
    }
  }
}
```

### Parameter `source`

**JSON:** required string `parameters.<template>.source`.

**Caddyfile:** second
`parameter` argument. Values: `path` or `query`;

**Default:** None.

Path placeholders must use `path`; query is for subject-only placeholders.

**Example:**
`"source":"path"`.

### Parameter `name`

**JSON:** required string `parameters.<template>.name`.

**Caddyfile:** third argument.

**Default:** None.

A path name equals its placeholder. A query name starts with an
ASCII letter and continues with letters, digits, `_`, `.`, or `-`.

**Example:**
`parameter term query q ^[A-Za-z0-9_-]+$`.

### Parameter `pattern`

**JSON:** required string `parameters.<template>.pattern`.

**Caddyfile:** fourth
argument.

**Default:** None.

It is a valid explicitly `^`/`$` anchored Go regexp, matches at least one character, and permits only ASCII letters, digits, `_`, or
`-`; unsafe operations fail. The entire value must match or HTTP returns `400`.

**Example:** `"pattern":"^[A-Za-z0-9_-]+$"`.

## Request policy

Request policy limits how much work one HTTP call can make the gateway and NATS
perform. In a typical JSON API, allow only the headers the service needs, set a
deadline that matches the service's expected response time, and choose body and
reply limits based on the largest legitimate message. Requests outside those
bounds are rejected instead of consuming unbounded memory or time.

### `request_headers`

**JSON:** optional string array `request_headers`.

**Caddyfile:**
`request_headers <Header>...`.

**Default:** Empty allowlist.

A route may inherit, replace, clear, or extend the list. Use standard HTTP capitalization, such as
`Content-Type`, and do not repeat names. Credential, cookie,
connection-specific, NATS, and identity-reserved headers are forbidden. Only
listed headers reach NATS.

**Example:**
`request_headers Content-Type Traceparent`.

### `timeout`

**JSON:** required positive duration `timeout`.

**Caddyfile:** `timeout <duration>`.

**Default:** None.

It may be inherited from a profile or removed with `clear`. It bounds protected
connection acquisition and NATS request/reply, or Core SSE subscription setup. HTTP
cancellation propagates. Expiry returns `504`; publication is never retried and
may already have occurred.

**Example:** `timeout 2s`.

### `max_request_body_bytes`

**JSON:** required positive integer `max_request_body_bytes`.

**Caddyfile:**
`max_request_body_bytes <bytes>`.

**Default:** None.

It may be inherited or cleared. It is a hard request bound; excess input returns `413` without publish.
Core SSE still requires an explicit bound.

**Example:**
`max_request_body_bytes 1048576`.

### `max_reply_bytes`

**JSON:** required positive integer `max_reply_bytes`.

**Caddyfile:**
`max_reply_bytes <bytes>`.

**Default:** None.

It may be inherited or cleared. It bounds a reply or each SSE event. Oversized replies return `502`; oversized
events close that stream with a bounded error event.

**Example:**
`max_reply_bytes 1048576`.

## Response policy

Every final route needs response settings. JSON groups them in `response`;
Caddyfile places the individual response directives directly in the route or
profile. A route can change one nested setting without repeating the others.

Choose `json` for API endpoints whose replies must contain valid JSON. Choose
`binary` for images, files, or other bytes the gateway should not interpret.
Then declare the exact HTTP content type and any ADR-32 application errors that
should become specific HTTP statuses. For example, an orders service can map
its `4041` “order not found” code to HTTP 404 without exposing the service's
error text.

### `response`

**JSON:** Required object `response`.

**Caddyfile:** There is no `response` object. Use the flattened directives
documented below.

**Default:** None.

Routes may inherit the object or remove it with `clear`; individual nested settings inherit
separately. Clearing the whole object requires restoring `mode` and
`content_type`.

**Example:**
`{"response":{"mode":"json","content_type":"application/json"}}`.

### `response.mode` / `response_mode`

**JSON:** required string `response.mode`.

**Caddyfile:** `response_mode <mode>`.

**Default:** None.

Routes may inherit it or remove it with `clear`. Values are
`json` and `binary`. JSON mode checks that the reply contains one valid JSON
value and returns `502` otherwise. Binary mode sends the bytes unchanged, still
subject to `max_reply_bytes`.

**Example:**
`response_mode json`.

### `response.headers` / `response_headers`

**JSON:** optional string array `response.headers`.

**Caddyfile:**
`response_headers <Header>...`.

**Default:** Empty allowlist.

A route may inherit, replace, clear, or extend the list. Use standard HTTP capitalization and unique
names. Credential, cookie, connection-specific, and NATS headers are forbidden.
Only listed reply headers reach HTTP.

**Example:** `response_headers ETag`.

### `response.content_type` / `response_content_type`

**JSON:** required string `response.content_type`.

**Caddyfile:**
`response_content_type <media-type>`.

**Default:** None.

Routes may inherit it or remove it with `clear`. Use the standard lowercase media type without
parameters or line breaks. JSON mode permits
`application/json` or `+json`. It becomes HTTP `Content-Type`, with
`X-Content-Type-Options: nosniff`; conflicting upstream type returns `502`.

**Example:** `response_content_type application/json`.

### `response.representations` / `response_representations`

**JSON:** optional string array `response.representations`.

**Caddyfile:**
`response_representations <media-type>...`.

**Default:** Empty list.

A route may inherit, replace, clear, or extend this list. Provide at least one entry when negotiation
is on, and none when it is off. Media types must be unique, including the main
content type, and must be JSON types in JSON mode. The gateway chooses a type;
it does not convert the reply.

**Example:**
`response_representations application/vnd.example.order+json`.

### `response.negotiate_accept` / `negotiate_accept`

**JSON:** optional boolean `response.negotiate_accept`.

**Caddyfile:**
`negotiate_accept <true|false>`.

**Default:** `false`.

Routes may inherit it or remove inherited `true` with `clear`. When true, representations are required.
The gateway reads a size-limited valid `Accept` header and chooses only a
declared type, which it sends to NATS. If none matches, it returns `406` before
publish.
To turn off inherited `true`, use `clear response.negotiate_accept` because a
JSON `false` merges like omission.

**Example:** `negotiate_accept true`.

### `response.service_error_statuses` / `service_error_status`

**JSON:** optional object `response.service_error_statuses` mapping code strings to
integer statuses.

**Caddyfile:** repeat `service_error_status <code> <status>`.

**Default:** Empty map.

A route may inherit, replace, clear, or extend the map. A code is a positive decimal of at most ten digits, with no leading zero; status is
`400`–`599`. Unmapped or malformed ADR-32 errors return `502`; descriptions are
not exposed.

**Example:** `service_error_status 4041 404`.

## Streaming settings

Use `request_reply` for ordinary calls that return one response. Use `core_sse`
for a live browser feed where receiving only events published while the browser
is connected is acceptable—for example, a dashboard showing current device
updates. Core SSE is not suitable for audit logs or resumable processing because
disconnects and slow consumers can lose messages.

### `stream_mode`

**JSON:** required string `stream_mode`.

**Caddyfile:** `stream_mode <mode>`.

**Default:** None.

Routes may inherit it or remove it with `clear`. `request_reply` sends one NATS request and waits for one reply, without retrying. `core_sse` requires
the settings below. Reserved `jetstream_sse` is not implemented and is rejected.

**Example:** `stream_mode request_reply`.

### `core_sse`

**JSON:** Object `core_sse`, required only for `core_sse` routes.

**Caddyfile:** There is no `core_sse` object. Use the flattened directives below.

**Default:** Absent.

It is forbidden in other modes, may be inherited or cleared, and inherits each nested setting separately. All
five fields are required. Core NATS SSE sends only messages seen while the
client is connected. It does not store, acknowledge, replay, or assign IDs to
events, and it does not support `Last-Event-ID`.

**Example:**

```json
{
  "core_sse": {
    "buffer_messages": 32,
    "buffer_bytes": 1048576,
    "heartbeat_interval": "15s",
    "max_duration": "15m",
    "max_connections": 100
  }
}
```

### `core_sse.buffer_messages` / `core_sse_buffer_messages`

**JSON:** required positive integer `core_sse.buffer_messages`.

**Caddyfile:**
`core_sse_buffer_messages <count>`.

**Default:** None.

It may be inherited from a profile or cleared. It limits each client's queued message count. Either
queue limit ends that stream as a slow consumer without blocking NATS.

**Example:**
`core_sse_buffer_messages 32`.

### `core_sse.buffer_bytes` / `core_sse_buffer_bytes`

**JSON:** required positive integer bytes `core_sse.buffer_bytes`.

**Caddyfile:**
`core_sse_buffer_bytes <bytes>`.

**Default:** None.

It may be inherited or cleared. Together with `buffer_messages`, it limits memory
used by each client's queue.

**Example:** `core_sse_buffer_bytes 1048576`.

### `core_sse.heartbeat_interval` / `core_sse_heartbeat_interval`

**JSON:** required positive duration `core_sse.heartbeat_interval`.

**Caddyfile:**
`core_sse_heartbeat_interval <duration>`.

**Default:** None.

It may be inherited or cleared. It must be less than `max_duration` and sends
SSE comment lines while no events are arriving, which helps keep proxies and
browsers from treating the connection as idle.

**Example:**
`core_sse_heartbeat_interval 15s`.

### `core_sse.max_duration` / `core_sse_max_duration`

**JSON:** required positive duration `core_sse.max_duration`.

**Caddyfile:**
`core_sse_max_duration <duration>`.

**Default:** None.

It may be inherited or cleared. It closes even healthy streams so one client cannot hold resources
forever. A protected stream closes sooner
at its connection's maximum lifetime.

**Example:** `core_sse_max_duration 15m`.

### `core_sse.max_connections` / `core_sse_max_connections`

**JSON:** required positive integer `core_sse.max_connections`.

**Caddyfile:**
`core_sse_max_connections <count>`.

**Default:** None.

It may be inherited or cleared. Routes that end up with the same Core SSE policy
share this per-handler HTTP stream limit. A newly reloaded handler has its own
limit. An excess connection returns `429` before subscribing.

**Example:**
`core_sse_max_connections 100`.

Invalid UTF-8, oversized events, or buffer exhaustion end only the affected
stream with a bounded error. Cancellation, cleanup, duration, and credential
expiry unsubscribe and release resources.

## Security contexts and credential adapters

Without `security_context`, a route uses the operator connection. With it, one
HTTP credential presentation creates or reuses an isolated NATS connection.
Use a security context when HTTP callers must keep their own NATS identity and
permissions. For example, Alice's bearer token must create a connection that
NATS authorizes as Alice; it must never reuse Bob's connection or fall back to a
more privileged gateway user.

NATS still decides whether the credential is valid and which subjects it may
use. The gateway keeps connections separate using a one-way fingerprint of the
exact credential. Routes with the same security policy share a bounded pool,
but different credentials remain separate entries. Changing security or
connection-lifetime policy creates a different pool, and a Caddy reload creates
new pools rather than sharing old mutable state.

### `security_context`

**JSON:** Optional object `security_context`.

**Caddyfile:** There is no `security_context` object. Use the flattened
directives below.

**Default:** Absent.

Routes may inherit or clear it, and each nested setting inherits
separately. When present, `mechanism`, `max_connections`, `idle_timeout`, and
`max_lifetime` are required.
Credential/authentication failure returns `401`, NATS permission failure `403`,
and pool/connectivity failure `503`.

**Example:**

```json
{
  "security_context": {
    "mechanism": "bearer_token",
    "max_connections": 100,
    "idle_timeout": "1m",
    "max_lifetime": "15m"
  }
}
```

### `security_context.mechanism` / `credential_mechanism`

**JSON:** required string `security_context.mechanism`.

**Caddyfile:**
`credential_mechanism <mechanism>`.

**Default:** None.

It may be inherited or cleared. Values are `bearer_token`, `user_password`, `nkey`, `nkey_jwt`, and
`tls`. Exactly one presentation is accepted; ambiguous input is rejected. The adapter presents proof;
NATS authenticates it.

**Example:** `credential_mechanism bearer_token`.

Presentations are: one Bearer `Authorization` value; HTTP Basic; or trusted
in-process NKey signing proof, NKey JWT/signing callbacks, or TLS client
certificate/private-key proof attached to the request context. Standard Caddy
HTTP configuration does not create the latter three, so they are conditionally
available only through an upstream integration. Mixed proofs fail as ambiguous.
Refreshed NKey JWT identity retires its old pool entry.

### `security_context.max_credential_bytes` / `max_credential_bytes`

**JSON:** optional non-negative integer bytes
`security_context.max_credential_bytes`.

**Caddyfile:**
`max_credential_bytes <bytes>`.

**Default:** `8192` when absent or zero.

Negative values fail validation. It limits each textual credential before connection;
oversize returns `401` without logging the value. Routes may inherit or clear
it; clearing restores the default.

**Example:** `max_credential_bytes 4096`.

### `security_context.max_connections` / `max_security_context_connections`

**JSON:** required positive integer `security_context.max_connections`.

**Caddyfile:**
`max_security_context_connections <count>`.

**Default:** None.

Routes may inherit or clear it. It limits pooled plus currently connecting NATS
connections for routes with the same security policy.
Idle entries may be evicted; otherwise new contexts return `503`. Aliased routes
cannot bypass the shared bound.

**Example:** `max_security_context_connections 100`.

### `security_context.idle_timeout` / `security_context_idle_timeout`

**JSON:** required positive duration `security_context.idle_timeout`.

**Caddyfile:**
`security_context_idle_timeout <duration>`.

**Default:** None.

Routes may inherit or clear it. A pooled connection that is not handling a
request closes after this idle time; a request in progress is not ended just
because the idle timer expires.

**Example:** `security_context_idle_timeout 1m`.

### `security_context.max_lifetime` / `security_context_max_lifetime`

**JSON:** required positive duration `security_context.max_lifetime`.

**Caddyfile:**
`security_context_max_lifetime <duration>`.

**Default:** None.

Routes may inherit or clear it. Connections stop accepting new requests at this
age, then close after current requests finish. Streams cannot outlive the
connection. Keep it within credential expiry and revocation requirements.

**Example:** `security_context_max_lifetime 15m`.

## Downstream identity

Use downstream identity when the NATS service needs to know which user NATS
actually authenticated—for example, to record who changed an order. This is
available only on protected `request_reply` routes.

The gateway asks NATS for the authenticated user on the request's own
connection, removes any caller-supplied value for the configured header, and
sends one trusted value to the service. It never treats a caller header, token
contents, or other unauthenticated claim as identity. The NATS user needs
publish permission to `$SYS.REQ.USER.INFO` and permission to subscribe to its
reply inbox. If identity lookup is denied, missing, malformed, or too large, the
gateway does not publish the application request. Identity values never enter
logs, metrics, errors, traces, or generated docs. See
[Downstream identity](downstream-identity.md).

### `security_context.downstream_identity`

**JSON:** Optional object `security_context.downstream_identity`.

**Caddyfile:** There is no `downstream_identity` object. Use the flattened
directives below.

**Default:** Absent.

Routes may inherit or clear it, and each nested setting inherits separately.
All three nested fields are required when
present. It is not available on streams, and its header cannot also appear in
`request_headers`.

**Example:**

```json
{
  "downstream_identity": {
    "source": "nats_user_id",
    "header": "X-Authenticated-User",
    "max_value_bytes": 256
  }
}
```

### `downstream_identity.source` / `downstream_identity_source`

**JSON:** required string `security_context.downstream_identity.source`.

**Caddyfile:**
`downstream_identity_source <source>`.

**Default:** None.

Routes may inherit or clear it. Only `nats_user_id` is supported.

**Example:**
`downstream_identity_source nats_user_id`.

### `downstream_identity.header` / `downstream_identity_header`

**JSON:** required string `security_context.downstream_identity.header`.

**Caddyfile:**
`downstream_identity_header <Header>`.

**Default:** None.

Routes may inherit or clear it. Use standard HTTP capitalization. It cannot be a credential,
connection-specific, or NATS-reserved header, and it cannot also appear in
`request_headers`. Caller values are stripped.

**Example:**
`downstream_identity_header X-Authenticated-User`.

### `downstream_identity.max_value_bytes` / `max_downstream_identity_bytes`

**JSON:** required positive integer bytes
`security_context.downstream_identity.max_value_bytes`.

**Caddyfile:**
`max_downstream_identity_bytes <bytes>`.

**Default:** None.

Routes may inherit or clear it. It limits the authenticated value; an oversized or invalid value
stops the request before the application message is published.

**Example:**
`max_downstream_identity_bytes 256`.

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

Run the standard integration target:

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
