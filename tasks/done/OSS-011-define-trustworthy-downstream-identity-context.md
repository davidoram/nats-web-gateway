# OSS-011 — Define trustworthy downstream identity context

## Why this is needed

A downstream NATS service may need to know which user NATS actually
authenticated. It must not trust an identity header supplied by the HTTP
caller, because a caller could simply claim to be another user or an
administrator.

This optional feature lets the gateway ask NATS for the user authenticated on
the request's own NATS connection. The gateway removes any caller-supplied value
for the configured identity header and sends the NATS-authenticated user to the
service instead. If NATS cannot provide a trustworthy identity, the gateway
does not send the application request. Routes that do not need downstream
identity can leave the feature disabled and continue relying on NATS account
and subject permissions for authorization.

## Identity and policy

Determine which authenticated identity attributes NATS exposes for each
supported mechanism, forward only protocol-authenticated and integrity-protected
context, prohibit caller-asserted identity, and document mechanisms for which
end-user identity propagation is unavailable.

Optionally propagate a configured authenticated identity attribute to NATS request/reply services as a gateway-generated message header. Configuration must declare both the supported identity source and the downstream header name.

Enable propagation only for authentication mechanisms where NATS exposes the attribute through a documented, integrity-protected protocol. If the configured mechanism cannot supply trustworthy identity, configuration must fail closed or the feature must remain unavailable. Never fall back to caller-supplied headers,
credential contents, token claims that the gateway has not authenticated, or connection success alone.

Validate the configured header name and reserve it from ordinary request-header forwarding. Strip any caller-supplied value with the same name before setting the gateway-generated value. Bound and validate identity values, prevent header injection, and exclude authenticated identity from logs, metrics, errors, traces, generated documentation, and other unintended outputs.

Keep credential presentation, connection isolation, and downstream identity assertion as separate security boundaries. OSS-009 adapters may describe their authentication mechanism and downstream-identity capability, while OSS-010 provides per-security-context connection isolation; neither connection success nor an adapter alone establishes a downstream identity claim.

Provide direct tests for:

- caller attempts to spoof the configured identity header;
- supported and unsupported authentication mechanisms;
- missing authenticated identity;
- malformed, injected, or oversized identity values;
- collisions with request-header allowlists and reserved headers;
- isolation across simultaneous users and overlapping Caddy reloads;
- absence of identity leakage in diagnostics and telemetry;
- the downstream service receiving exactly the NATS-authenticated identity.

## Completion evidence

- [PR #12](https://github.com/davidoram/nats-web-gateway/pull/12)
