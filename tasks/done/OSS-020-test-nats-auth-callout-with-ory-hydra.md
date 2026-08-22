# OSS-020 — Test NATS Auth Callout with Ory Hydra

## Authentication integration

Add an end-to-end integration test proving that the gateway's
`bearer_token` credential adapter works with a real NATS custom Auth Callout whose identity provider is Ory Hydra. This task must exercise the complete authentication and authorization path; a static NATS token configuration or a mocked NATS connection is not sufficient.

Extend the pinned, local integration environment with:

- an Ory Hydra server suitable for deterministic test use;
- the matching pinned Hydra CLI;
- a test-only OAuth2 client created and configured through the Hydra CLI;
- a custom NATS Auth Callout service; and
- least-privilege NATS accounts, signing keys, users, and subject permissions required by the callout service and application fixture.

Use the Hydra CLI during test setup to create the OAuth2 client and configure the client-credentials grant, allowed scopes, audience, and other values needed by the fixture. Obtain access tokens from Hydra's OAuth2 token endpoint. The Auth Callout service must treat the bearer value received through the NATS authorization request as opaque, validate it against Hydra using OAuth2 token introspection, and return a correctly signed NATS authorization response. It must derive only the minimum NATS user/account assignment and publish/subscribe permissions needed for the declared test route. Neither the gateway nor the callout may parse an access token as proof of validity without Hydra validation.

Exercise the successful path through every real boundary:

1. start Hydra, NATS, the custom Auth Callout service, the application service, and Caddy with the gateway module;
2. configure the OAuth2 client through the Hydra CLI;
3. request an access token from Hydra;
4. send that token to a protected HTTP route as a bearer credential;
5. verify NATS invokes the custom Auth Callout and accepts its signed response;
6. verify the request reaches only the permitted NATS subject and the expected HTTP response is returned; and
7. verify the gateway does not validate the OAuth2 token itself or forward the HTTP `Authorization` header to the application service.

Provide direct negative and failure tests for:

- a missing bearer credential;
- malformed and oversized bearer presentations;
- a random or unknown token;
- an expired or revoked/inactive Hydra token;
- a token with the wrong audience or insufficient scope;
- Hydra introspection being unavailable or timing out;
- a malformed, unsigned, incorrectly signed, expired, or replayed Auth Callout response where the NATS protocol permits the case to be induced;
- an Auth Callout response granting no access to the route's subject;
- attempts to publish or subscribe outside the issued permissions;
- no fallback to the gateway's operator connection, static authentication, or a broader NATS account after any authentication or authorization failure; and
- absence of access tokens, client secrets, authorization payloads, and authenticated identity attributes from logs, errors, test output, and other diagnostics.

Assert that authentication rejection is mapped to the gateway's documented minimal HTTP response and that failure classes remain distinguishable in safe internal diagnostics. Confirm cleanup of OAuth clients, processes, NATS connections, subscriptions, temporary state, and containers after both passing and failing tests.

Pin all container images and CLI versions deliberately. Use only loopback or the isolated test network, checked-in non-production fixture material, bounded timeouts, readiness checks, and deterministic setup and teardown. The tests must run without public internet services and must be included in the standard integration and CI verification paths.

Document the integration-test architecture and local execution command. Make clear that the fixture demonstrates one deployment-selected NATS authentication mechanism and does not make the gateway an OAuth2/OIDC verifier or identity provider.

## Verification

Run and report:

- `go tool mage verify`;
- `go tool mage integration`;
- `go tool mage security`;
- `go tool mage ci`; and
- race-enabled tests for the custom Auth Callout service and gateway connection lifecycle exercised by the fixture.

## Dependencies

- OSS-003 — Build the local integration environment.
- OSS-009 — Implement HTTP-to-NATS credential adapters.
- OSS-010 — Enforce per-security-context NATS authorization.

## Completion evidence

- [PR #14](https://github.com/davidoram/nats-web-gateway/pull/14)
