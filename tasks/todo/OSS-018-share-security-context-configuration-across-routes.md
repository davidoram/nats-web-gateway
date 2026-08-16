# OSS-018 — Share security-context configuration across routes

## Configuration and identity policy

Allow operators to declare reusable, named `security_context` configurations
once and reference them from multiple routes, while permitting an individual
route to override explicitly selected fields when its requirements differ. The
goal is to remove repetitive credential-adapter and connection-lifecycle
configuration without weakening authentication, authorization, tenant
isolation, resource bounds, or fail-closed validation.

Define equivalent JSON and Caddyfile syntax for:

- top-level named security-context definitions;
- route references to one named definition;
- optional route-level overrides; and
- existing fully inline route configuration for compatibility.

Specify and document deterministic inheritance and merge semantics. Overrides
must be explicit at field granularity; omitted fields inherit from the named
definition, and ambiguous combinations, unknown references, duplicate names,
inheritance cycles, partially resolved configurations, and invalid effective
values must fail before serving traffic. Do not introduce implicit fallback to
the operator connection or to another credential mechanism.

Routes whose effective security-context configuration and named context are the
same should share that named context's bounded connection pool, allowing the
same authenticated HTTP security context to reuse its identity-bound NATS
connection across those routes. A route override that changes any
security-sensitive or lifecycle field must resolve to a distinct pool unless
the implementation can prove the effective configurations and ownership
boundaries are identical. Distinct credential presentations must never share a
connection, even when their routes reference the same named configuration.

Preserve per-handler ownership so overlapping Caddy instances do not share
mutable pools. Apply maximum connection cardinality, idle timeout, maximum
lifetime, reconnect behavior, cleanup, authentication failure, and permission
failure consistently across every route using a shared pool. Define whether
connection limits apply per named context or per handler, make the choice
visible in configuration and documentation, and prevent route aliases or
overrides from multiplying or bypassing the configured bound.

Maintain backward compatibility for OSS-010 inline `security_context`
configuration. Document precedence, migration examples, and the operational
effect of changing or removing a named context during Caddy reload.

Provide direct tests for:

- multiple routes inheriting one named security context;
- field-level route overrides and deterministic precedence;
- equivalent JSON and Caddyfile adaptation;
- unknown, duplicate, ambiguous, cyclic, incomplete, and invalid definitions;
- same-credential connection reuse across routes sharing one named context;
- isolation between different credentials and overridden contexts;
- shared cardinality and lifetime bounds that cannot be bypassed with routes;
- authentication and subject-permission failures without operator fallback;
- simultaneous requests, reconnects, expiry, cleanup, and race detection; and
- overlapping Caddy reloads owning independent pools without credential,
  connection, or configuration leakage.

## Dependencies

- OSS-010 — Enforce per-security-context NATS authorization.
