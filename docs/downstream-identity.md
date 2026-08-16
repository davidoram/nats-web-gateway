# Downstream identity

The gateway can optionally add one NATS-authenticated identity attribute to a
request/reply message. It does not infer identity from HTTP headers,
credentials, token contents, adapter selection, or successful connection
establishment.

## Supported source

`nats_user_id` is the only supported source. For every application request, the
gateway asks `$SYS.REQ.USER.INFO` over the same credential-isolated NATS
connection that will publish the application message. NATS generates this
response from the connection's authenticated server-side user and account
state. The gateway accepts only the response's non-empty `data.user` value.

The NATS deployment must configure a system account so the per-user information
service is available. Each protected NATS identity also needs permission to
publish `$SYS.REQ.USER.INFO` and subscribe to its request inbox. These
permissions expose only that connection's own server-authenticated information;
they do not grant access to arbitrary system-account subjects.

| HTTP credential adapter | NATS user ID availability |
| --- | --- |
| `bearer_token` | Available only when NATS, commonly through Auth Callout, authenticates the connection and `$SYS.REQ.USER.INFO` returns a non-empty user ID. The gateway never parses the bearer token. |
| `user_password` | Available through the NATS user-info response. The HTTP Basic username is not used as the assertion. |
| `nkey` | Available through the NATS user-info response after nonce authentication. The presented public NKey is not used as the assertion. |
| `nkey_jwt` | Available through the NATS user-info response after JWT and nonce authentication. The gateway does not inspect JWT claims. |
| `tls` | Available through the NATS user-info response after NATS completes mutual TLS authentication. Certificate fields are not used as the assertion. |
| Unprotected/operator routes | Unavailable. Downstream identity configuration is nested in a protected security context. |
| Any other source or inferred attribute | Unsupported and rejected during configuration validation. |

## Validation and failure behavior

Configuration declares the source, a canonical downstream message-header name,
and a positive maximum identity size. HTTP and NATS protocol headers cannot be
selected. The generated header is reserved from the route's ordinary request
header allowlist.

The user-info response has a fixed 64 KiB parsing bound. The selected identity
must be valid UTF-8, non-empty, within the configured byte limit, and free of
control characters, including carriage return, line feed, and NUL. Before the
application publish, the gateway removes every caller-supplied value for the
configured header and adds exactly one validated value.

If the NATS user-info service is unavailable, denied, times out, reports an
error, omits the user, or returns malformed or invalid data, the gateway does
not publish the application request. Deadline expiry maps to `504`; other
identity-resolution failures map to the same bounded `503` response used for
unavailable upstream infrastructure. Public errors contain no identity value.

Identity is queried for every request rather than inferred once per pooled
connection. This preserves the authenticated boundary across reconnects,
credential rotation, simultaneous users, and overlapping Caddy instances.
Identity values are used only in the configured NATS message header and must
not be logged, traced, used as metric labels, or copied into HTTP responses or
generated output.
