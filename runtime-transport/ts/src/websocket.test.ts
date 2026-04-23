import { describe, it, expect, vi } from 'vitest';
import { createWebSocketTransport, type WebSocketTransport } from './websocket';
import { tryDecodeRuntimeEnvelope } from './binaryEnvelope';

// Mock WebSocket
class MockWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;

  onopen: (() => void) | null = null;
  onclose: ((ev: any) => void) | null = null;
  onmessage: ((ev: { data: string | ArrayBuffer }) => void) | null = null;
  onerror: (() => void) | null = null;
  readyState = 0; // CONNECTING
  binaryType: 'blob' | 'arraybuffer' = 'blob';

  constructor(public url: string) {
    setTimeout(() => {
      this.readyState = 1; // OPEN
      this.onopen?.();
    }, 10);
  }

  send(data: string | ArrayBuffer) {
    this.sentMessages.push(data);
    
    // Simulate server response for subscriptions
    try {
      const envelope = tryDecodeRuntimeEnvelope(data);
      const eventType = envelope.eventType;
      const correlationId = envelope.metadata.correlationId;
      const pattern = (envelope.payload as any)?.pattern;
      
      if (eventType === 'system:websocket_subscribe:v1:requested') {
        setTimeout(() => {
          this.onmessage?.({
            data: JSON.stringify({
              event_type: 'system:websocket_subscribe:v1:success',
              payload: { pattern },
              metadata: { correlation_id: correlationId },
              correlation_id: correlationId,
              schema_version: 1,
              timestamp: new Date().toISOString()
            })
          });
        }, 5);
      }
      if (eventType === 'system:websocket_unsubscribe:v1:requested') {
        setTimeout(() => {
          this.onmessage?.({
            data: JSON.stringify({
              event_type: 'system:websocket_unsubscribe:v1:success',
              payload: { pattern },
              metadata: { correlation_id: correlationId },
              correlation_id: correlationId,
              schema_version: 1,
              timestamp: new Date().toISOString()
            })
          });
        }, 5);
      }
    } catch (e) {}
  }
  
  sentMessages: any[] = [];
  close = vi.fn();
}

(global as any).WebSocket = MockWebSocket;

describe('WebSocketTransport Resilience', () => {
  it('should track and re-register subscriptions on reconnect', async () => {
    const sockets: MockWebSocket[] = [];
    // Binary-first transport
    const transport = createWebSocketTransport({ 
      url: 'ws://localhost:8080',
      createSocket: (url) => {
        const ws = new MockWebSocket(url);
        sockets.push(ws);
        return ws as any;
      }
    }) as Required<WebSocketTransport>;
    
    // Subscribe
    const callback = vi.fn();
    const sub = await transport.subscribe('media:*', callback);
    expect(sockets.length).toBe(1);
    const hasSubscribed = sockets[0].sentMessages.some((m: any) => {
      const s = typeof m === 'string' ? m : new TextDecoder().decode(m);
      return s.includes('system:websocket_subscribe:v1:requested') && s.includes('media:*');
    });
    expect(hasSubscribed).toBe(true);

    // Simulate connection drop
    sockets[0].onclose?.({ code: 1006, reason: 'abnormal', wasClean: false } as CloseEvent);

    // Wait for reconnect attempt (binary transport uses reconnect by default)
    await new Promise(resolve => setTimeout(resolve, 300));

    // Verify re-subscription request sent on new WS
    expect(sockets.length).toBe(2);
    const hasReSubscribed = sockets[1].sentMessages.some((m: any) => {
      const s = typeof m === 'string' ? m : new TextDecoder().decode(m);
      return s.includes('system:websocket_subscribe:v1:requested') && s.includes('media:*');
    });
    expect(hasReSubscribed).toBe(true);
  });

  it('should cleanup subscriptions on unsubscribe', async () => {
    const sockets: MockWebSocket[] = [];
    const transport = createWebSocketTransport({ 
      url: 'ws://localhost:8080',
      createSocket: (url) => {
        const ws = new MockWebSocket(url);
        sockets.push(ws);
        return ws as any;
      }
    }) as Required<WebSocketTransport>;
    
    await new Promise(resolve => setTimeout(resolve, 50));

    const sub = await transport.subscribe('media:*', () => {});
    sub.unsubscribe();

    // Verify unsubscription request sent
    const hasUnsubscribed = sockets[0].sentMessages.some((m: any) => {
      const s = typeof m === 'string' ? m : new TextDecoder().decode(m);
      return s.includes('system:websocket_unsubscribe:v1:requested') && s.includes('media:*');
    });
    expect(hasUnsubscribed).toBe(true);
  });
});
