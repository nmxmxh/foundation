import {
  Component,
  Suspense,
  lazy,
  useCallback,
  useMemo,
  useState,
  type ComponentType,
  type ReactNode,
} from "react";

import { createChunkGate, DEFAULT_CHUNK_TIMEOUT_MS } from "./chunkGate";

export interface LazyPageOptions {
  /**
   * Timeout for the chunk request. The guard converts hung fetches into a
   * retryable error boundary instead of an eternal loader.
   */
  timeoutMs?: number;
  /** Shown while the chunk is pending. */
  fallback?: ReactNode;
}

type PageModule<C extends ComponentType<unknown>> = { default: C };

interface RetryBoundaryProps {
  children: ReactNode;
  fallback?: ReactNode;
  onRetry: () => void;
}

class ChunkRetryBoundary extends Component<RetryBoundaryProps, { failed: boolean }> {
  override state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  override render() {
    if (this.state.failed) {
      return (
        <div
          role="alert"
          style={{ alignItems: "center", display: "flex", flexDirection: "column", gap: "0.75rem", padding: "3rem 1rem" }}
        >
          <p style={{ margin: 0 }}>This page failed to load.</p>
          <button type="button" onClick={this.props.onRetry}>
            Retry
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}

const DefaultPendingFallback = () => <div role="status">Loading…</div>;

/**
 * Wraps a dynamic route import in the chunk timeout gate. Route chunks must
 * load through this helper; bare lazy(() => import(...)) routes have no
 * recovery path when a fetch stalls.
 */
export const createLazyPage = <C extends ComponentType<unknown>>(
  load: () => Promise<PageModule<C>>,
  { timeoutMs = DEFAULT_CHUNK_TIMEOUT_MS, fallback }: LazyPageOptions = {}
): ComponentType<Record<string, unknown>> => {
  const gate = createChunkGate(load, timeoutMs);

  return function LazyPage(props: Record<string, unknown>) {
    // A rejected lazy() caches its failure forever, so retry rebuilds the
    // component instead of reusing it.
    const [attempt, setAttempt] = useState(0);
    const Content = useMemo(
      () => lazy(() => gate.load()) as unknown as ComponentType<Record<string, unknown>>,
      [attempt]
    );
    const retry = useCallback(() => {
      gate.reset();
      setAttempt((current) => current + 1);
    }, []);

    return (
      <ChunkRetryBoundary key={attempt} onRetry={retry}>
        <Suspense fallback={fallback ?? <DefaultPendingFallback />}>
          <Content {...props} />
        </Suspense>
      </ChunkRetryBoundary>
    );
  };
};
