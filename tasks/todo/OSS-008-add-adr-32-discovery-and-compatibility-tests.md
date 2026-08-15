# OSS-008 — Add ADR-32 discovery and compatibility tests

## HTTP request/reply

Verify interoperability with the NATS Service API defined by ADR-32 using pinned,
real NATS and Caddy processes and services implemented with the supported Go
NATS service library.

Test the `PING`, `INFO`, and `STATS` discovery subjects at all ADR-32 scopes:

- all services: `$SRV.<verb>`;
- services with a specified name: `$SRV.<verb>.<name>`;
- one service instance: `$SRV.<verb>.<name>.<id>`.

Validate the standard response type, service name, stable and unique instance ID,
semantic version, description, immutable service metadata, endpoint names,
subjects, queue groups, endpoint metadata, start time, request and error counts,
processing time, average processing time, and last-error information.

Run multiple instances of the same service and verify:

- discovery returns every matching instance;
- each endpoint request receives exactly one reply;
- queue subscriptions do not duplicate request handling;
- aggregate statistics reflect the requests and errors exercised;
- stopping or draining an instance removes it from subsequent discovery;
- another healthy instance continues serving requests.

Discovery subjects are NATS control-plane interfaces. Test them directly through
least-privilege NATS fixtures; do not expose `$SRV.*` as arbitrary HTTP gateway
routes.

## Example services

Provide runnable Go services and matching Caddy configurations using bounded JSON
payloads:

1. A REST-style Pets API with:
   - `POST /pets`;
   - `GET /pets`;
   - `GET /pets/{id}`;
   - `PUT /pets/{id}`;
   - `DELETE /pets/{id}`.

2. An RPC-style Pets API with:
   - `POST /rpc/pets.CreatePet`;
   - `POST /rpc/pets.GetPet`;
   - `POST /rpc/pets.UpdatePet`;
   - `POST /rpc/pets.DeletePet`;
   - `POST /rpc/pets.ListPets`.

Use concurrency-safe in-memory state, deterministic validation and ADR-32
application errors, declared subjects, least-privilege fixture permissions, and
explicit request/reply size limits. Document that example state is non-production
and resets on restart.

Exercise every example through real HTTP to Caddy to gateway to NATS service and
back to HTTP functional tests. Assert status, content type, response body, error
mapping, discovery metadata, and service statistics.

Do not add native gRPC transport in this task. gRPC framing, protobuf contracts,
trailers, streaming, and status mapping require a separate architecture decision
and implementation task.

## Verification

Run and report:

- `go tool mage verify`;
- `go tool mage integration`;
- `go tool mage security`;
- `go tool mage ci`.

Tests must be deterministic, race-safe, independent of public internet services,
and must not assume fair distribution among queue-group members.
