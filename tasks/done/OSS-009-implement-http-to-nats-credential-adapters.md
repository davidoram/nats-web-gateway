# OSS-009 — Implement HTTP-to-NATS credential adapters

## Identity and policy

Present explicitly supported HTTP credentials as NATS client authentication
options without validating identity in the gateway; cover Auth Callout
bearer-token, user/password, NKey/JWT, and TLS mappings where their
proof-of-possession semantics can be preserved; reject unsupported or ambiguous
mappings.

## Completion evidence

- [PR #10](https://github.com/davidoram/nats-web-gateway/pull/10)
