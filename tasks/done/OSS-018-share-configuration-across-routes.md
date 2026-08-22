# OSS-018 — Share configuration across routes

## Configuration

Allow operators to declare reusable, named route configuration once and apply it to multiple routes. Reuse must cover every route setting for which sharing is meaningful, rather than being designed around `security_context` alone. This includes, but is not limited to:

- `request_headers`;
- `timeout`;
- `max_request_body_bytes`;
- `max_reply_bytes`;
- `response`, including `mode`, `headers`, `content_type`, `representations`, `negotiate_accept`, and `service_error_statuses`;
- streaming policy such as `stream_mode` and `core_sse`; and
- `security_context`, including credential-adapter, connection-lifecycle, and downstream-identity settings.

Inventory the complete route schema when designing the feature. Explicitly classify which fields may be shared, which must remain route-specific, and why.
At minimum, route identity and matching fields such as `name`, `path`, and `methods` must remain explicit wherever inheritance could make the resulting HTTP surface difficult to audit. Subject templates and their parameters may be shareable only if the resulting subjects remain declared, unambiguous, and fail-closed.

Define equivalent JSON and Caddyfile syntax for:

- top-level named reusable route configurations;
- a route referencing one named configuration;
- explicit route-level overrides; and
- existing fully inline route configuration for compatibility.

Specify deterministic, field-level inheritance and merge semantics for scalar, list, map, and nested-object values. Define how an operator explicitly replaces,clears, or extends an inherited value; omission must have one unambiguous meaning. Unknown references, duplicate names, inheritance cycles, incompatible combinations, partially resolved configurations, and invalid effective routes must fail validation before serving traffic. Validation and runtime behavior must operate on a fully resolved effective route, and generated/adapted JSON must make that result inspectable without exposing secrets.

Preserve the architectural guarantees of every shared setting. In particular:

- header forwarding remains allowlisted and reserved headers cannot be inherited into an unsafe combination;
- request/reply limits and deadlines remain explicit and bounded;
- response and service-error mapping remains deterministic;
- streaming configurations retain their buffer, lifetime, slow-consumer, and connection bounds; and
- security contexts retain credential isolation, fail-closed authentication and authorization, and per-handler ownership across overlapping Caddy instances.

Where routes resolve to the same effective named security context, retain the safe cross-route connection reuse and bounded-pool behavior described by OSS-010. An override that changes a security-sensitive or connection-lifecycle field must resolve to a distinct pool unless effective configurations and ownership boundaries are proven identical. Distinct credential presentations must never share a connection. Route aliases, profiles, or overrides must not multiply or bypass configured cardinality and lifetime bounds.

Maintain backward compatibility for existing inline route configuration. Document precedence, migration examples, the operational effect of changing or removing shared configuration during Caddy reload, and how operators can inspect the effective configuration of each route.

Provide direct tests for:

- multiple routes inheriting request headers, limits, timeouts, response behavior, streaming policy, and security-context settings;
- field-level overrides, including deterministic replacement, clearing, and extension semantics for every supported value shape;
- equivalent JSON and Caddyfile adaptation;
- the documented classification of shareable and route-specific fields;
- unknown, duplicate, ambiguous, cyclic, incomplete, incompatible, and invalid definitions;
- validation of fully resolved routes before traffic is served;
- preservation of header, payload, response, streaming, and identity security boundaries after inheritance;
- same-credential connection reuse and isolation between different credentials or overridden contexts;
- shared cardinality and lifetime bounds that cannot be bypassed with routes;
- simultaneous requests, reconnects, expiry, cleanup, and race detection; and
- overlapping Caddy reloads owning independent mutable state without credential, connection, or configuration leakage.

## Dependencies

- OSS-010 — Enforce per-security-context NATS authorization.
- OSS-012 — Implement Core NATS to SSE streaming.

## Completion evidence

- [PR #15](https://github.com/davidoram/nats-web-gateway/pull/15)
