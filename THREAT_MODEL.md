# Threat model

## Scope and security objectives

This threat model covers the open-source NATS Web Gateway described by
`ARCHITECTURE.md`: a Caddy HTTP handler that translates explicitly configured
HTTP routes into NATS request/reply or streaming operations. It covers the
gateway, its configuration and secrets, its NATS connections, and the protocol
boundaries with HTTP clients, credential adapters, Caddy, NATS, and
application services.

The identity provider, Caddy, NATS server, and application services are external
systems. Their internal security is out of scope, but the gateway must behave
safely when any untrusted peer sends malformed input or becomes unavailable.
Compromise of the host, Caddy process, trusted secret source, NATS operator, or
the identity backend selected by NATS (including an Auth Callout service) is
outside the protection offered by the gateway.

The security objectives, in priority order, are:

1. Prevent cross-tenant access and publishing or subscribing outside declared
   routes and least-privilege NATS permissions.
2. Preserve explicit request/reply and stream delivery semantics, including
   cancellation and distinguishable failures.
3. Bound memory, time, connections, subscriptions, payloads, and diagnostic
   cardinality under malicious or accidental load.
4. Protect credentials, NATS connection state, identity attributes, payloads,
   and internal routing details from unauthorized disclosure or injection.
5. Fail closed when configuration, authentication, authorization, identity
   verification, or protocol interpretation is ambiguous.

## System and data flows

```text
Untrusted HTTP client
        |
        | HTTP/TLS: credentials, route parameters, headers, body
        v
   Caddy HTTP lifecycle ---- trusted configuration / secret sources
        |
        | validated route plus untrusted credential presentation
        v
 NATS Web Gateway handler / credential adapter
        |
        | per-security-context CONNECT credentials
        v
 NATS server ---- optional Auth Callout / configured NATS auth mechanism
        |
        | authenticated connection with account and subject permissions
        v
 Gateway ---- declared subject and bounded payload ---- service / stream
        |
        | reply, service error, live message, or persisted message
        v
 NATS Web Gateway -> bounded HTTP response or SSE -> HTTP client
```

The gateway creates or propagates a non-spoofable request identifier and may
emit bounded metrics, traces, and redacted structured logs. Observability
backends are data recipients, not authorization authorities.

## Trust boundaries

| Boundary | Untrusted or less-trusted input | Required enforcement |
| --- | --- | --- |
| Internet to Caddy/gateway | Method, URL, route parameters, query, headers, credential presentation, body, disconnect timing | TLS policy belongs to Caddy; match only declared routes; validate templates; allowlist headers; enforce size, time, stream, and connection limits; treat credentials as untrusted until NATS accepts them |
| Gateway credential adapter to NATS CONNECT | Bearer token, user/password, token, NKey/JWT, TLS material, or another configured presentation | Use only an explicit mechanism-specific mapping; preserve proof-of-possession semantics; isolate connections by security context; bound connection caches; fail closed when mapping or NATS authentication fails |
| NATS server to Auth Callout or other identity backend | Connection metadata, credentials, authorization request/response, account and permission claims | This is a deployment-controlled trust boundary; authenticate the callout service and signer, encrypt callout traffic where configured, validate signed responses, assign least privilege, and fail closed on timeout or malformed response |
| Gateway to authenticated NATS connection | Constructed subject, headers, payload | Construct subjects only from declared templates and validated values; NATS account and subject permissions are authoritative; never retry through a broader identity after denial |
| NATS/service to gateway | Replies, ADR-32 headers, stream messages, timing, disconnects | Treat as untrusted protocol input; bound and validate replies; map errors deterministically; enforce backpressure and cleanup |
| Gateway to application service | Request metadata and any end-user identity context | Never forward caller-asserted identity; propagate attributes only when NATS exposes them through a defined integrity-protected mechanism, otherwise assert no end-user identity |
| Configuration/secret source to process | Routes, credential-adapter configuration, NATS trust material and credentials | Restrict administrative access; validate atomically; do not serialize secrets; support overlapping reload instances without shared mutable state |
| Gateway to logs/metrics/traces | Errors, identifiers, route outcomes | Redact tokens, cookies, credentials, authenticated identity attributes, and sensitive payloads; use bounded labels; do not expose raw subjects, tenants, users, or paths as metric labels |

## Assets

- NATS authentication results, account assignment, subject permissions, and tenant isolation
- bearer credentials, passwords, NKey/JWT material, TLS private keys, and live authenticated connections
- end-user identity attributes exposed by a configured NATS authentication mechanism
- request and response payload confidentiality and integrity
- declared route-to-subject mappings and internal subject topology
- availability of Caddy, gateway resources, NATS connections, and services
- delivery semantics, acknowledgements, resume positions, and request outcomes
- configuration integrity and reload consistency
- audit, trace, and request-correlation integrity

## Attacker capabilities and assumptions

An unauthenticated Internet attacker can issue concurrent requests, choose all
caller-controlled HTTP bytes, abort at arbitrary times, replay observed client
requests, and attempt slow reads or writes. An authenticated malicious user can
exercise every route granted to that identity and attempt tenant escape,
subject-template injection, claim spoofing, response confusion, quota evasion,
and resource exhaustion.

A malicious or compromised NATS publisher/service can send malformed, oversized,
late, duplicated, or misleading replies and stream messages. A network fault can
produce equivalent timing, reconnect, and partial-failure behavior. Operators
can make mistakes in route, permission, key, or timeout configuration.

The design assumes administrators of the host, Caddy configuration, trusted
secret sources, NATS operator/account, and configured NATS identity backend are
trusted. It also assumes TLS and NATS transport security are configured for the
deployment threat environment. Trust in those systems does not permit trusting
identity or routing assertions supplied by an HTTP client.

NATS is the sole authentication and authorization authority. Credential
adapters do not establish identity: they only present credentials to NATS. An
Auth Callout deployment may validate OAuth credentials against previously
registered users and assign their NATS account and permissions; another
deployment may use any supported NATS authentication and authorization method.
The gateway supports a method only when its HTTP-to-NATS mapping preserves the
method's security semantics.

## Threats and required controls

| Threat | Security effect | Required controls and verification |
| --- | --- | --- |
| Caller chooses or injects a NATS subject through path/query/template syntax | Cross-tenant publish/subscribe or control-plane access | Only declared routes and subject templates; strict parameter grammar and wildcard policy; configuration ambiguity rejected; least-privilege NATS publish/subscribe permissions; parser/property tests and integration denial tests |
| Gateway accepts a credential locally or falls back after NATS rejects it | Authentication or tenant-boundary bypass | NATS connection success is the only authentication result; no local OIDC/user validation or broader fallback identity; real-NATS rejection tests for every adapter |
| Connection pool or cache reuses a NATS connection across security contexts | One user receives another user's account or subject permissions | Security-context-bound keys, expiry and revocation-aware lifetimes, bounded caches, credential-change invalidation, concurrency/race tests, and no shared protected-route connection |
| Credential adapter turns proof-of-possession material into a bearer assertion | Credential theft or impersonation | Mechanism-specific mappings; never infer NKey signatures or mutual-TLS possession from headers; explicitly reject mappings that cannot preserve the original proof |
| Caller copies trusted-looking identity headers or fields | Identity or tenant impersonation at an application service | Strip internal identity headers; do not infer claims from NATS connection success; forward identity only from a defined integrity-protected NATS mechanism; negative tests |
| Forged, replayed, expired, or malformed Auth Callout response | Unauthorized NATS connection or permissions | NATS-configured issuer verification; optional XKey encryption; one-time response key behavior; strict time and claim validation; least-privilege user claims; callout integration tests |
| Header smuggling, hop-by-hop forwarding, CR/LF injection, or ADR-32 error spoofing | Request confusion, credential leakage, cache/proxy abuse | Explicit end-to-end header allowlists; never forward hop-by-hop, credential, or internal identity headers implicitly; validate NATS header names/values; gateway-owned response/error mapping; fuzz tests |
| Oversized/slow body, reply, or stream; excessive connections/subscriptions | Memory, goroutine, connection, or NATS exhaustion | Hard request/reply limits; bounded stream buffers and quotas; deadlines, idle and maximum-duration limits; slow-consumer policy; cancellation cleanup; load, race, and leak tests |
| Disconnect, timeout, reconnect, reload, or late reply races | Leaks, duplicate work, misreported outcomes | Propagate context; explicit ownership and stop paths; retries off by default; overlapping module instances without globals; deterministic timeout/cancel/no-responder mapping; race and integration tests |
| Treating Core NATS as durable or JetStream delivery as exactly-once | Silent data loss or duplicate side effects | Expose distinct configured modes; document guarantees at route and operator surfaces; never imply equivalence; test resume, redelivery, acknowledgement, retention, and disconnect behavior |
| Malformed or hostile service reply and ADR-32 headers | HTTP response injection, incorrect status, disclosure | Bound payload before allocation/use; validate protocol fields and status ranges; deterministic safe fallback; preserve internal cause only in redacted diagnostics; integration and fuzz tests |
| Permission errors or service topology disclosed to callers | Subject enumeration and privilege discovery | Preserve failure class internally while returning the documented minimal response; do not expose raw subjects, credentials, or server details |
| Malicious configuration or unsafe reload | Broad access, outage, mixed credentials, or cross-context reuse | Configuration is an administrative trust boundary; validate routes and credential mappings before serving; NATS validates permissions; atomic instance replacement; no shared mutable globals; readiness distinguishes invalid configuration/connectivity |
| Sensitive or high-cardinality telemetry | Credential/PII disclosure or observability outage | Structured redaction tests; no tokens, cookies, credentials, authenticated identity attributes, or payloads; bounded route/result labels; internally generated correlation identifiers |
| Dependency or artifact compromise | Process compromise or altered release | Deliberate pins, vulnerability review, reproducible builds, SBOM, checksums, and release scanning before v0.1.0 |

## Protocol failure mapping

The gateway owns HTTP status selection. It must validate service input and must
not copy an arbitrary NATS or ADR-32 value directly into an HTTP status line or
response header. Response bodies are bounded and use a stable, non-sensitive
gateway error representation. Detailed causes belong in redacted diagnostics.

| Condition | HTTP outcome | Delivery and disclosure rule |
| --- | --- | --- |
| Route or method is not declared | `404 Not Found` or Caddy route fallthrough | Do not reveal whether a related NATS subject exists |
| Credential is absent, cannot be mapped safely, or NATS rejects connection authentication | `401 Unauthorized` | NATS is the authentication authority; challenge details are adapter/configuration controlled; never echo credential material or callout errors |
| The authenticated NATS connection lacks permission for the configured operation | `403 Forbidden` | NATS is the authorization authority; preserve permission-denied cause internally; do not disclose account, subject, or permission details |
| Request body exceeds the configured hard limit | `413 Content Too Large` | Do not publish a partial request |
| Request or template input is syntactically invalid | `400 Bad Request` | Do not publish; return only safe field-level context |
| No NATS responder exists | `503 Service Unavailable` | Distinct from timeout; execution did not obtain a responder, but callers must not infer broad topology |
| Gateway deadline expires waiting for a reply | `504 Gateway Timeout` | No automatic retry; the service may still have observed or executed the request |
| Client cancels or disconnects | Abort the response; record a distinct internal cancellation outcome | Cancel the NATS wait and clean up; HTTP has no portable final status once disconnected; service execution may already have begun |
| NATS connectivity is unavailable or interrupted before a valid reply | `503 Service Unavailable` | No automatic retry; distinguish from no responders and permission denial internally |
| Valid, explicitly mapped ADR-32 service error code | Route-configured HTTP status, restricted to `400`–`599` | Treat `Nats-Service-Error-Code` as an application code, never an HTTP status; use an explicit code-to-status mapping; expose only configured safe text |
| Valid but unmapped ADR-32 service error code | `502 Bad Gateway` | Preserve the application code in bounded internal diagnostics; do not expose arbitrary service text by default |
| Reply or service-error metadata is malformed | `502 Bad Gateway` | Never pass malformed status/header data through |
| Reply exceeds the configured hard limit | `502 Bad Gateway` | Stop/abort bounded processing and record response-limit cause; do not truncate as a successful reply |
| Unexpected gateway or NATS internal failure | `500 Internal Server Error` or `503 Service Unavailable` when specifically an availability failure | Stable public error; preserve wrapped cause internally without secrets |

Because an HTTP timeout, cancellation, or lost connection cannot prove that a
NATS service did not execute a request, request/reply is at-most-one gateway
publish with an ambiguous application outcome after publication. Retries remain
disabled by default and require an explicitly safe/idempotent route policy in a
future architecture-approved design.

## Messaging guarantees

### Core NATS live mode

Core NATS is ephemeral, best-effort, at-most-once delivery to an active
subscription. A client receives only messages observed while its gateway
subscription is active. There is no acknowledgement, persistence, replay,
resume, or recovery of messages lost during disconnect, reload, reconnect, or
buffer overflow. Queue subscriptions, if later exposed, additionally mean one
member receives a message rather than every member.

### JetStream resumable mode

JetStream persists messages subject to configured stream retention and supports
consumer acknowledgement, redelivery, and a defined resume/start position. It
is therefore at-least-once across the gateway-to-consumer boundary when
configured accordingly: duplicates are possible, retention or limits can remove
unconsumed messages, and acknowledgement timing defines the loss/redelivery
trade-off. `Last-Event-ID` is not sufficient by itself; consumer ownership,
event-ID stability, acknowledgement timing, retention constraints, and cleanup
must be specified and tested by OSS-013 before the mode is released. The gateway
must never claim exactly-once end-to-end processing.

The two modes require explicit route configuration and distinct documentation,
metrics, tests, and operator expectations. See
[`docs/adr/0002-distinct-core-nats-and-jetstream-modes.md`](docs/adr/0002-distinct-core-nats-and-jetstream-modes.md).

## Delegated authentication and downstream identity

The gateway treats credentials as opaque until NATS accepts a connection. NATS
then enforces the assigned account and publish/subscribe permissions for every
operation. See
[`docs/adr/0003-delegate-authentication-and-authorization-to-nats.md`](docs/adr/0003-delegate-authentication-and-authorization-to-nats.md).

Connection success proves only what the configured NATS mechanism establishes;
it does not authenticate arbitrary HTTP claims or message headers. The gateway
must not manufacture a signed identity envelope from caller input. OSS-011 must
identify any authenticated attributes NATS exposes through a trustworthy
protocol on a per-mechanism basis. Where none exist, downstream services rely on
the NATS account and subject authorization and receive no gateway assertion of
end-user identity.

## Residual risks and deferred validation

- A compromised trusted administrator, host, NATS identity backend, secret
  source, NATS account/operator, or application service can exceed gateway
  protections.
- Publication followed by timeout, cancellation, or connection loss has an
  inherently ambiguous application outcome; the gateway cannot promise that no
  side effect occurred.
- Incorrect NATS account, Auth Callout, or permission configuration can widen
  impact. Integration tests and deployment guidance must verify the documented
  identities and permission set.
- Per-security-context connections can amplify connection load; caches and
  lifetimes must be bounded without allowing cross-context reuse.
- Some NATS authentication mechanisms do not expose authenticated end-user
  attributes to application messages; authorization can remain correct while
  downstream personalization or user-level auditing is unavailable.
- Core NATS loss and JetStream duplication/retention are protocol properties,
  not failures the gateway can eliminate.
- Denial of service can be bounded per gateway instance but not eliminated;
  deployment-level connection, CPU, network, and upstream controls remain
  necessary.

Each implementation task must turn the applicable controls above into direct
unit, integration, fuzz, race, load, redaction, or compatibility assertions as
required by `ARCHITECTURE.md` §9. Threat-model changes that weaken an objective,
expand a trust assumption, or alter a protocol decision require explicit review
and, where they conflict with `ARCHITECTURE.md`, a dedicated architecture PR.
