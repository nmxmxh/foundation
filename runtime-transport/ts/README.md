# Runtime-Transport (TypeScript)

The `runtime-transport` package is the unified communication layer for Ovasabi frontends. It handles both request-response (HTTP) and push-based (WebSocket) patterns with built-in resilience.

## Core Concepts

### 1. Command Bus
The central dispatch mechanism. It uses a `TransportStrategy` to decide how an envelope is delivered.
- **HTTP Transport**: Direct connection for commands and queries. Support for binary streams.
- **WebSocket Transport**: Persistent connection for real-time events and high-frequency updates.

### 2. Resilience: Re-Subscription logic
Network drops should not cause state loss. The `WebSocketTransport` implements a **Re-Subscription Map**.
- When you subscribe to a pattern (for example, `media:*`), the client stores this in an internal registry.
- Upon reconnection, it automatically clears the server state and re-sends all active subscription requests.

### 3. Ingress Optimization
Large request bodies (for example, file uploads, large state patches) are automatically intercepted and compressed using **GZIP**. This is transparent to the developer.

## LLM Agent Patterns: Command Dispatch

```typescript
const transport = createHTTPTransport({ baseUrl: 'https://api.ovasabi.com' });
const bus = createCommandBus(transport);

// Agents should always wrap payloads in the execution envelope
const result = await bus.dispatch({
  eventType: 'identity:login:v1:requested',
  payload: { email, password },
  metadata: { correlationId: '...' }
});
```

### Subscribing to events
```typescript
const sub = bus.subscribe('media:*', (envelope) => {
  console.log('Media Progress:', envelope.payload);
});

// To stop listening:
sub.unsubscribe();
```
