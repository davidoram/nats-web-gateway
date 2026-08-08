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
        "mode": "raw",
        "headers": ["Content-Type"],
        "content_type": "application/octet-stream",
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
    response_mode raw
    response_headers Content-Type
    response_content_type application/octet-stream
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
- `response.mode` currently supports `raw`. A configured content type is used
  when a safe upstream content type is not forwarded.
- ADR-32 service error codes are positive decimal application codes. They map
  only to explicitly configured HTTP statuses from 400 through 599; they are
  never treated directly as HTTP statuses.
- `stream_mode` is mandatory and distinguishes `request_reply`, `core_sse`, and
  `jetstream_sse`. The streaming modes intentionally remain distinct; their
  bounded runtime policies are implemented by OSS-012 and OSS-013.

JSON duration values use Caddy duration strings such as `250ms` or `2s`. Byte
limits are decimal integers in both JSON and the Caddyfile.

## Request/reply example

The runnable [Go orders service](../examples/orders-service/main.go) handles a
lookup and a create operation. These routes expose it without allowing callers
to choose arbitrary NATS subjects:

```caddyfile
nats_web_gateway {
  nats_urls nats://127.0.0.1:4222
  connect_timeout 5s
  reconnect_wait 1s
  max_reconnects -1
  drain_timeout 30s
  route get_order {
    path /api/orders/{id}
    methods GET
    subject orders.get.{id}.{view}
    parameter id path id ^[A-Za-z0-9_-]+$
    parameter view query view ^[A-Za-z0-9_-]+$
    timeout 2s
    max_request_body_bytes 1024
    max_reply_bytes 65536
    response_mode raw
    response_headers Content-Type
    response_content_type application/json
    stream_mode request_reply
  }
  route create_order {
    path /api/orders
    methods POST
    subject orders.create
    request_headers Content-Type Traceparent
    timeout 2s
    max_request_body_bytes 65536
    max_reply_bytes 65536
    response_mode raw
    response_headers Content-Type
    response_content_type application/json
    stream_mode request_reply
  }
}
```

Run the service with `go run ./examples/orders-service`, then exercise both
behaviors through Caddy:

```text
curl 'http://localhost:8080/api/orders/order-42?view=confirmed'
curl -X POST -H 'Content-Type: application/json' \
  --data '{"id":"order-43","status":"pending"}' \
  http://localhost:8080/api/orders
```

The first request publishes to `orders.get.order-42.confirmed`. Query values
must appear exactly once and all path/query values must match their configured
grammar. The second forwards only `Content-Type` and `Traceparent`; credentials,
cookies, hop-by-hop headers, and caller-asserted identity are never copied.
