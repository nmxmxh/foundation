import { describe, it, expect, vi } from 'vitest';
import { createWebSocketTransport, type WebSocketTransport } from './websocket';
import { encodeJSONRuntimeEnvelope, tryDecodeRuntimeEnvelope } from './binaryEnvelope';
import { createEnvelope } from './index';

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
    await new Promise(resolve => setTimeout(resolve, 10));

    // Verify unsubscription request sent
    const hasUnsubscribed = sockets[0].sentMessages.some((m: any) => {
      const s = typeof m === 'string' ? m : new TextDecoder().decode(m);
      return s.includes('system:websocket_unsubscribe:v1:requested') && s.includes('media:*');
    });
    expect(hasUnsubscribed).toBe(true);
  });

  it('reference-counts duplicate subscription patterns', async () => {
    const sockets: MockWebSocket[] = [];
    const transport = createWebSocketTransport({ url: 'ws://localhost:8080', createSocket: url => {
      const socket = new MockWebSocket(url);
      sockets.push(socket);
      return socket as any;
    } });
    const first = await transport.subscribe!('media:*', vi.fn());
    const second = await transport.subscribe!('media:*', vi.fn());
    expect(sockets[0].sentMessages.filter(message => String(new TextDecoder().decode(message as ArrayBuffer)).includes('websocket_subscribe'))).toHaveLength(1);
    first.unsubscribe();
    expect(sockets[0].sentMessages.some(message => String(new TextDecoder().decode(message as ArrayBuffer)).includes('websocket_unsubscribe'))).toBe(false);
    second.unsubscribe();
    await Promise.resolve();
    expect(sockets[0].sentMessages.some(message => String(new TextDecoder().decode(message as ArrayBuffer)).includes('websocket_unsubscribe'))).toBe(true);
    transport.close();
  });

  it('resolves and rejects correlated JSON dispatch terminal envelopes', async () => {
    const sockets: MockWebSocket[] = [];
    const transport = createWebSocketTransport({ url: 'ws://localhost:8080', preferBinary: false, reconnect: { enabled: false }, createSocket: (url) => {
      const socket = new MockWebSocket(url);
      sockets.push(socket);
      return socket as any;
    } });
    const route = { method: 'POST', path: '/x', eventType: 'asset:get:v1:requested', requiredCapability: '', permission: 'view' as const };
    const firstEnvelope = createEnvelope({ eventType: route.eventType, payload: {} });
    const first = transport.dispatch(firstEnvelope, route, new AbortController().signal);
    await new Promise(resolve => setTimeout(resolve, 20));
    sockets[0].onmessage?.({ data: encodeJSONRuntimeEnvelope({ ...firstEnvelope, eventType: 'asset:get:v1:success', payload: { id: 1 } }) });
    await expect(first).resolves.toEqual({ id: 1 });

    const secondEnvelope = createEnvelope({ eventType: route.eventType, payload: {} });
    const second = transport.dispatch(secondEnvelope, route, new AbortController().signal);
    await Promise.resolve();
    await new Promise(resolve => setTimeout(resolve, 0));
    sockets[0].onmessage?.({ data: encodeJSONRuntimeEnvelope({ ...secondEnvelope, eventType: 'asset:get:v1:failed', payload: { reason: 'denied' } }) });
    await expect(second).rejects.toThrow('denied');
    expect(transport.isConnected()).toBe(true);
    transport.close(1000, 'done');
    expect(transport.getConnectionState()).toBe('closed');
  });

  it('rejects pre-aborted and in-flight dispatch and pending requests on close', async () => {
    const sockets: MockWebSocket[] = [];
    const transport = createWebSocketTransport({ url: 'ws://localhost:8080', reconnect: { enabled: false }, createSocket: (url) => {
      const socket = new MockWebSocket(url);
      sockets.push(socket);
      return socket as any;
    } });
    const route = { method: 'POST', path: '/x', eventType: 'asset:get:v1:requested', requiredCapability: '', permission: 'view' as const };
    const already = new AbortController();
    already.abort();
    await expect(transport.dispatch(createEnvelope({ eventType: route.eventType, payload: {} }), route, already.signal)).rejects.toThrow('aborted');

    const controller = new AbortController();
    const aborted = transport.dispatch(createEnvelope({ eventType: route.eventType, payload: {} }), route, controller.signal);
    await new Promise(resolve => setTimeout(resolve, 20));
    controller.abort();
    await expect(aborted).rejects.toThrow('aborted');

    const pending = transport.dispatch(createEnvelope({ eventType: route.eventType, payload: {} }), route, new AbortController().signal);
    await new Promise(resolve => setTimeout(resolve, 0));
    transport.close();
    await expect(pending).rejects.toThrow('closed');
  });

  it('honors readiness envelopes and invokes the ready hook once', async () => {
    const sockets: MockWebSocket[] = [];
    const onReady = vi.fn(async () => undefined);
    const transport = createWebSocketTransport({
      url: 'ws://localhost:8080', preferBinary: false, reconnect: { enabled: false },
      readyWhenEnvelope: envelope => envelope.eventType === 'system:ready:v1:success', onReady,
      createSocket: (url) => { const socket = new MockWebSocket(url); sockets.push(socket); return socket as any; },
    });
    const subscribing = transport.subscribe!('asset:*', vi.fn());
    await new Promise(resolve => setTimeout(resolve, 20));
    expect(transport.getConnectionState()).toBe('connecting');
    sockets[0].onmessage?.({ data: JSON.stringify({ event_type: 'system:ready:v1:success', payload: {}, metadata: {}, schema_version: 1 }) });
    await subscribing;
    expect(onReady).toHaveBeenCalledTimes(1);
    expect(transport.getConnectionState()).toBe('open');
    transport.close();
  });

  it('bounds connection failure without reconnect when disabled', async () => {
    class FailedSocket extends MockWebSocket {
      constructor(url: string) {
        super(url);
        queueMicrotask(() => this.onerror?.());
      }
    }
    const transport = createWebSocketTransport({ url: 'ws://localhost:8080', reconnect: { enabled: false }, createSocket: url => new FailedSocket(url) as any });
    await expect(transport.subscribe!('*', vi.fn())).rejects.toThrow('connection failed');
    expect(transport.getConnectionState()).toBe('closed');
  });
});
