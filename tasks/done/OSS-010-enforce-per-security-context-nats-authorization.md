# OSS-010 — Enforce per-security-context NATS authorization

## Identity and policy

Create and isolate NATS connections by authenticated security context, preserve
account and subject permissions, prevent credential or connection reuse across
contexts, bound connection cardinality and lifetime, and map authentication and
permission failures safely.

## Completion evidence

- [PR #11](https://github.com/davidoram/nats-web-gateway/pull/11)
