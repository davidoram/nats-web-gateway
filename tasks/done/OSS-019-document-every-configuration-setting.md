# OSS-019 — Document every configuration setting

## Documentation

Update `docs/configuration.md` so it contains a dedicated section for every configuration setting supported by the gateway. This task is documentation-only:
`docs/configuration.md` is the only file that may change. Do not add, remove, rename, or change the runtime behavior of configuration fields.

Build the inventory from the implemented configuration structs, validation, JSON schema exposed by the Caddy module, and Caddyfile adapter rather than from the existing document alone. Cover top-level NATS settings, reusable configuration introduced by OSS-018, route matching and subject construction, request headers, request/reply limits and timeouts, response modes and error mapping, streaming settings, security contexts, credential adapters, connection lifecycle, and downstream identity. Document JSON-only, Caddyfile-only, nested, and conditionally available settings explicitly.

Each setting's section must state:

- its exact JSON key and Caddyfile syntax, or that it is unavailable in one representation;
- its type, format, units, default, and whether it is required;
- where it is valid and which modes or other settings enable it;
- validation constraints and mutually exclusive or dependent settings;
- inheritance, precedence, replacement, and clearing behavior where reusable configuration applies;
- its runtime effect, including relevant failure behavior;
- security, privacy, resource-use, and reload implications where applicable; and
- a minimal valid example.

Organize the reference so operators can navigate from a complete configuration example to top-level and nested setting sections without guessing where a field is documented. As completion evidence, include in the pull request a manual coverage audit comparing the documented setting inventory with the implemented
JSON and Caddyfile configuration surfaces.

Review all examples and cross-references in `docs/configuration.md` for accuracy, including equivalent JSON and Caddyfile forms. Do not include literal production credentials or expose secret values in examples, generated output, or test fixtures.

## Dependencies

- OSS-018 — Share configuration across routes.

## Completion evidence

- [PR #16](https://github.com/davidoram/nats-web-gateway/pull/16)
