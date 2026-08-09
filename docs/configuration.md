# Gateway configuration

The `nats_web_gateway` HTTP handler exposes only operations declared by an
operator. Configuration is validated before Caddy serves traffic. Empty,
incomplete, ambiguous, or unsafe route sets are rejected.

Configuration describes connection lifecycle and translation policy. A request
whose method and path do not match a declared route passes to the next Caddy
handler. A matched `request_reply` route publishes exactly once and never
retries automatically.

## JSON

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
  "routes": [
    {
      "name": "get_order",
      "path": "/orders/{id}",
      "methods": ["GET"],
      "subject": "orders.{id}",
      "parameters": {
        "id": {
          "source": "path",
          "name": "id",
          "pattern": "^[A-Za-z0-9_-]+$"
        }
      },
      "request_headers": ["Content-Type", "Traceparent"],
      "timeout": "2s",
      "max_request_body_bytes": 1048576,
      "max_reply_bytes": 1048576,
      "response": {
        "mode": "json",
        "content_type": "application/json",
        "service_error_statuses": {"4001": 400}
      },
      "stream_mode": "request_reply"
    }
  ]
}
```

## Caddyfile

```caddyfile
nats_web_gateway {
  nats_urls nats://127.0.0.1:4222
  nats_user gateway
  nats_password {env.NATS_GATEWAY_PASSWORD}
  connect_timeout 5s
  reconnect_wait 1s
  max_reconnects -1
  drain_timeout 30s
  route get_order {
    path /orders/{id}
    methods GET
    subject orders.{id}
    parameter id path id ^[A-Za-z0-9_-]+$
    request_headers Content-Type Traceparent
    timeout 2s
    max_request_body_bytes 1048576
    max_reply_bytes 1048576
    response_mode json
    response_content_type application/json
    service_error_status 4001 400
    stream_mode request_reply
  }
}
```

## NATS connection lifecycle

The `nats` block is required. `urls` contains one or more explicit `nats`,
`tls`, `ws`, or `wss` server URLs without embedded credentials. The initial
connection must authenticate successfully before Caddy accepts the
configuration. A disconnected or reconnecting connection is not ready; a
successful reconnect restores readiness. `max_reconnects: -1` retries for the
life of the module instance, while a non-negative value bounds retry attempts.

Each handler instance owns its connection. During a Caddy reload, the new and
old instances may overlap without sharing mutable state. Cleanup first stops
readiness, drains subscriptions and buffered publishes for at most
`drain_timeout`, and then forces closure if the drain does not finish. Reloads
do not retry application requests or change their delivery semantics.

Use Caddy placeholders backed by an appropriate secret source for `username`
and `password`; do not put literal production credentials in JSON, Caddyfiles,
logs, examples, or generated documentation. This operator connection is for
routes without an end-user security context and must have only the publish and
inbox-subscribe permissions required by the declared routes. Protected routes
will use distinct credential-adapted connections; the gateway never expands
the permissions NATS grants.

Each `parameter` has four arguments: template name, HTTP source (`path` or
`query`), HTTP field name, and an explicitly anchored regular expression. A
path placeholder must use a matching path source. A subject-only placeholder
may use a query source, for example:

```caddyfile
parameter term query q ^[A-Za-z0-9_-]+$
```

## Validation and semantics

- Route names are unique. Routes whose path shapes can match the same request
  and whose method sets overlap are rejected.
- Paths are absolute and parameters occupy complete path segments.
- Subjects contain non-empty dot-separated tokens. Wildcards, whitespace, and
  caller-selected subjects are forbidden; parameters occupy complete tokens.
- Every parameter has exactly one declared source and an anchored grammar that
  excludes subject and path separators, wildcards, and whitespace.
- Methods are explicit, uppercase HTTP tokens. Timeouts and both byte limits
  must be greater than zero.
- Header names are canonical and unique. Credential, hop-by-hop, NATS protocol,
  and caller-asserted identity headers cannot be forwarded even if listed.
- `response.mode` is explicitly `json` or `binary`. JSON replies must contain
  one syntactically valid JSON value. Binary replies remain opaque but bounded.
- `response.content_type` is required, canonical, parameter-free, and becomes
  the HTTP `Content-Type`; `X-Content-Type-Options: nosniff` is always set on a
  successful response. If the service supplies `Content-Type`, it must exactly
  match the selected representation or the reply is rejected as malformed.
- Optional `negotiate_accept` considers only `content_type` and the media types
  in `representations`. It honors exact media ranges, type and global
  wildcards, quality values, exclusions, and declaration order for ties. An
  invalid header or no acceptable declared representation returns `406` before
  publishing. The selected type is sent to the service as `Accept`; the gateway
  never infers or transcodes an undeclared format.
- ADR-32 service error codes are positive decimal application codes. They map
  only to explicitly configured HTTP statuses from 400 through 599; they are
  never treated directly as HTTP statuses.
- `stream_mode` is mandatory and distinguishes `request_reply`, `core_sse`, and
  `jetstream_sse`. The streaming modes intentionally remain distinct; their
  bounded runtime policies are implemented by OSS-012 and OSS-013.

JSON duration values use Caddy duration strings such as `250ms` or `2s`. Byte
limits are decimal integers in both JSON and the Caddyfile.

## Request/reply examples

The runnable [Go orders service](../examples/orders-service/main.go) stores
orders in a concurrency-safe in-memory map. `POST /api/orders` creates or
replaces the order with the supplied `id` and `status`; `GET /api/order/{id}`
returns its current status. These routes expose it without allowing callers to
choose arbitrary NATS subjects:

```caddyfile
nats_web_gateway {
  nats_urls nats://127.0.0.1:4222
  connect_timeout 5s
  reconnect_wait 1s
  max_reconnects -1
  drain_timeout 30s
  route get_order {
    path /api/order/{id}
    methods GET
    subject orders.get.{id}
    parameter id path id ^[A-Za-z0-9_-]+$
    timeout 2s
    max_request_body_bytes 1024
    max_reply_bytes 65536
    response_mode json
    response_content_type application/json
    response_representations application/vnd.example.order+json
    negotiate_accept true
    service_error_status 4041 404
    stream_mode request_reply
  }
  route create_or_replace_order {
    path /api/orders
    methods POST
    subject orders.create
    request_headers Content-Type Traceparent
    timeout 2s
    max_request_body_bytes 65536
    max_reply_bytes 65536
    response_mode json
    response_content_type application/json
    service_error_status 4001 400
    stream_mode request_reply
  }
}
```

The runnable [Go image service](../examples/images-service/main.go) provides a
binary PNG route. This is also included in the complete
[example Caddyfile](../examples/Caddyfile):

```caddyfile
route logo {
  path /assets/logo.png
  methods GET
  subject images.logo
  timeout 2s
  max_request_body_bytes 1024
  max_reply_bytes 1048576
  response_mode binary
  response_content_type image/png
  stream_mode request_reply
}
```

Build the components by running:

```bash
 go tool mage integration
```

Run the system as follows:

```bash
podman-compose --file compose.yml up --detach
podman-compose --file compose.yml down --volumes --remove-orphans
```

Docker users can replace `podman-compose` with `docker compose`.

Then to call the NATS service via HTTP can run:

```bash
curl -X POST -H 'Content-Type: application/json' \
  --data '{"id":"order-42","status":"pending"}' \
  http://localhost:8080/api/orders
curl 'http://localhost:8080/api/order/order-42'
curl -H 'Accept: application/vnd.example.order+json' \
  'http://localhost:8080/api/order/order-42'
curl -X POST -H 'Content-Type: application/json' \
  --data '{"id":"order-42","status":"shipped"}' \
  http://localhost:8080/api/orders
curl 'http://localhost:8080/api/order/order-42'
curl -H 'Accept: image/png' http://localhost:8080/assets/logo.png --output logo.png
```

The POST requests publish to `orders.create`; the second overwrites the first
order. The GET requests publish to `orders.get.order-42`; they return
`{"status":"pending"}` before the overwrite and `{"status":"shipped"}` after
it. Unknown IDs return the configured `404`, while malformed order payloads
return `400`. Create requests forward only `Content-Type` and `Traceparent`;
credentials, cookies, hop-by-hop headers, and caller-asserted identity are never
copied. Enabling negotiation adds only the gateway-selected `Accept` value. The
map is intentionally process-local example state and is empty after a service
restart.

Configurations from the earlier request/reply scaffold must replace
`response_mode raw` with the explicit `json` or `binary` mode and declare
`response_content_type`. This repository has not released a stable version yet;
there is no runtime fallback for the old mode because ambiguous response
handling must fail during configuration loading.

## Deterministic failures

Gateway errors use a bounded JSON body and never copy NATS subjects, service
descriptions, credentials, or payloads. The initial mapping is: invalid route
input `400`; authentication failure `401`; permission denial `403`; unacceptable
representation `406`; oversized request `413`; malformed, oversized, or
unmapped ADR-32 reply `502`; no responders or unavailable connectivity `503`;
and deadline expiry `504`. A mapped ADR-32 application code uses only its
route-declared `400`–`599` status. Client cancellation aborts the wait and does
not manufacture a final HTTP response. Requests are published at most once and
are never retried automatically, so timeout or cancellation after publication
remains an ambiguous application outcome.
