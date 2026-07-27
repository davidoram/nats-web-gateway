# ADR 0002: Keep Core NATS and JetStream streaming modes distinct

- Status: Accepted
- Date: 2026-07-27
- Applies to: Architecture §§2, 3, 6, 9–10

## Context

Core NATS subscriptions and JetStream consumers can both feed Server-Sent
Events, but they do not provide equivalent delivery. Presenting both behind an
undifferentiated "stream" option would conceal loss, duplication, persistence,
acknowledgement, resume, and ownership behavior from operators and clients.

## Decision

SSE is the initial HTTP streaming transport, with two explicitly configured
NATS modes:

- **Core NATS live:** ephemeral, best-effort, at-most-once delivery while the
  subscription is active. It provides no acknowledgement, persistence, replay,
  or resume guarantee.
- **JetStream resumable:** persistent, consumer-based, at-least-once delivery
  when configured accordingly. It can redeliver duplicates, and retention or
  limits can remove messages before consumption.

Mode names, configuration, documentation, metrics, event IDs, and tests remain
distinct. Neither mode claims exactly-once end-to-end processing. All streams
enforce bounded buffers, connection quotas, maximum duration, idle handling,
slow-consumer behavior, cancellation, and deterministic subscription/consumer
cleanup.

JetStream will not ship until OSS-013 defines and tests consumer ownership,
start position, stable event IDs and `Last-Event-ID`, acknowledgement timing,
redelivery, retention constraints, disconnect behavior, and cleanup. A Core
NATS disconnect or overflow cannot be represented as resumable delivery.

## Consequences

- Operators and clients must select a mode based on explicit loss, latency,
  persistence, duplication, and resource trade-offs.
- Some configuration and implementation paths are duplicated to keep semantics
  honest.
- JetStream requires more lifecycle state and tests than live Core NATS.
- Backpressure remains bounded in both modes; persistence does not justify an
  unbounded gateway buffer.

## Alternatives rejected

- **One generic streaming mode:** rejected because a common interface would
  obscure guarantees or collapse them to misleading lowest-common-denominator
  language.
- **Simulated resume for Core NATS:** rejected because the gateway cannot replay
  messages it never persisted.
- **Exactly-once claims for JetStream:** rejected because acknowledgement,
  redelivery, client disconnects, and downstream side effects permit duplicates
  or ambiguous outcomes.
- **JetStream for every stream:** rejected because ephemeral low-latency live
  subscriptions remain useful and should not require persistence infrastructure.
