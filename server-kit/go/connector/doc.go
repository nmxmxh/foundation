// Package connector provides a transport-agnostic outbound "API hook" for
// connecting Ovasabi services to external APIs.
//
// A Connector is the outbound counterpart to the inbound platform surface that
// the rest of server-kit owns. Where handlers/workers/events deal with traffic
// arriving at a service, a Connector deals with traffic a service originates
// toward some remote system that may speak any protocol: REST/HTTP, gRPC,
// WebSocket, GraphQL, or anything else expressible as a Driver.
//
// # Design goals
//
//   - Agnostic   - callers code against Connector/Driver, never a protocol.
//   - Aware      - capabilities (encoding/version/features) are negotiated and
//     cached; runtime context (correlation/idempotency) flows through.
//   - Status     - active probing plus passive call-outcome observation, tracked
//     as a tri-state-plus health model.
//   - Healing    - degrade, retry with jittered backoff, trip/recover via the
//     shared circuitbreaker, fail over across transports, reconnect streams.
//   - Streaming  - resumable streams that re-establish from the last watermark.
//
// # The intelligent thread (MAPE-K)
//
// Each Connector owns a single long-lived supervisor goroutine implementing a
// MAPE-K control loop (Monitor -> Analyze -> Plan -> Execute over shared
// Knowledge). The supervisor probes the remote, classifies health against SLO
// thresholds, decides on a healing action, and applies it. Status snapshots are
// emitted so callers and dashboards can observe the connector without polling
// the remote directly.
//
// # Composition
//
// The connector orchestrates existing server-kit primitives rather than
// reinventing them:
//
//	circuitbreaker - trip/recover semantics around Call/Stream
//	metrics        - counters/histograms for calls, probes, transitions
//	slo            - breach evaluation feeding the Analyze step
//	chaos          - fault injection for tests and game-days
//
// Protocol drivers live in subpackages (httpx, grpcx, wsx, graphqlx) and
// implement the Driver interface defined here. The core package imports none of
// them, so adding a transport never touches core.
package connector
