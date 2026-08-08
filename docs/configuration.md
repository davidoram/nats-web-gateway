# Gateway configuration

The `nats_web_gateway` HTTP handler exposes only operations declared by an
operator. Configuration is validated before Caddy serves traffic. Empty,
incomplete, ambiguous, or unsafe route sets are rejected.

Configuration currently describes translation policy; request execution is
introduced by OSS-005 through OSS-008. Until then, the handler validates its
configuration and passes requests to the next Caddy handler.

## JSON

```json
{
  "handler": "nats_web_gateway",
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
