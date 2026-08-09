# OSS-007 — Implement deterministic response and error mapping

## HTTP request/reply

Define how each declared Caddy route maps a NATS service reply to an HTTP
response. Support explicit, fail-closed response modes for structured JSON and
binary payloads while preserving bounded reply sizes, safe content types, and
deterministic handling of malformed replies.

Optionally support standards-compliant HTTP `Accept` negotiation when enabled
for a route. Negotiate only among representations explicitly declared by the
operator, including media ranges, wildcards, quality values, and deterministic
`406 Not Acceptable` behavior; do not infer or transcode undeclared formats.

Provide realistic Go examples and matching Caddy configuration for a JSON API
response and a binary PNG image response. Link the examples from the project
README so users can see both representation modes and optional `Accept` header
behavior end to end.

Cover no responders, timeout, cancellation, permission failure, malformed
replies, payload limits, and ADR-32 `Nats-Service-Error` headers.

## Completion evidence

- [PR #8](https://github.com/davidoram/nats-web-gateway/pull/8)
