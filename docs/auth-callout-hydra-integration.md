# Ory Hydra-backed NATS Auth Callout integration

The OSS-020 fixture proves the full protected-request boundary:

```text
HTTP bearer credential -> gateway credential adapter -> NATS CONNECT
  -> custom Auth Callout -> Hydra introspection
  -> signed least-privilege NATS user JWT -> auth.echo -> HTTP response
```

The gateway only presents the opaque bearer value to NATS. It does not parse,
validate, introspect, or authorize OAuth2 tokens. The custom deployment-selected
Auth Callout performs introspection and issues a one-minute NATS user claim that
may publish only to `auth.echo` and subscribe only to its request inbox. The
application fixture rejects any accidentally forwarded HTTP `Authorization`
header.

Run the fixture through the canonical integration target:

```text
go tool mage integration
```

The target builds the custom Caddy binary and both Go fixtures, starts the
isolated compose project, creates the non-production client with the pinned
Hydra CLI, obtains an opaque client-credentials token, exercises success and
fail-closed authentication cases, and always removes containers and volumes.
The same target is included in `go tool mage ci`.

All credentials and signing material under `integration/auth-callout/` are
public test fixtures. Never reuse them. The stack binds host ports only to
loopback, uses bounded timeouts, and does not call a public service while tests
run. Container images and Go dependencies are deliberately pinned in the
compose file and module graph.
