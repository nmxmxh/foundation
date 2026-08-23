import { useCallback, useEffect, useState, type ComponentType, type ReactNode } from "react";

import { createChunkGate, DEFAULT_CHUNK_TIMEOUT_MS } from "./chunkGate";

export interface LazyPageOptions {
  /**
   * Timeout for the chunk request. The guard converts hung fetches into a
   * retryable failure state instead of an eternal loader.
   */
  timeoutMs?: number;
  /** Shown while the chunk is pending. */
  fallback?: ReactNode;
}

type PageModule<C extends ComponentType<unknown>> = { default: C };

type LoadState<C extends ComponentType<unknown>> =
  | { status: "pending" }
  | { status: "failed"; cause: unknown }
  | { status: "ready"; component: C };

const DefaultPendingFallback = () => <div role="status">Loading…</div>;

/**
 * Wraps a dynamic route import in the chunk timeout gate. Route chunks must
 * load through this helper; bare lazy(() => import(...)) routes have no
 * recovery path when a fetch stalls.
 *
 * Fully functional by policy: the gate promise drives state instead of
 * React.lazy throwing through an error boundary, so a failed load renders a
 * visible cause plus retry rather than relying on class-component machinery.
 */
export const createLazyPage = <C extends ComponentType<unknown>>(
  load: () => Promise<PageModule<C>>,
  { timeoutMs = DEFAULT_CHUNK_TIMEOUT_MS, fallback }: LazyPageOptions = {}
): ComponentType<Record<string, unknown>> => {
  const gate = createChunkGate(load, timeoutMs);

  return function LazyPage(props: Record<string, unknown>) {
    // A failed attempt must not poison the next one, so retry both resets
    // the gate and bumps the epoch this effect depends on.
    const [attempt, setAttempt] = useState(0);
    const [state, setState] = useState<LoadState<C>>({ status: "pending" });

    useEffect(() => {
      let live = true;
      setState({ status: "pending" });
      gate
        .load()
        .then((module) => {
          if (live) setState({ status: "ready", component: module.default });
        })
        .catch((cause) => {
          if (live) setState({ status: "failed", cause });
        });
      return () => {
        live = false;
      };
    }, [attempt]);

    const retry = useCallback(() => {
      gate.reset();
      setAttempt((current) => current + 1);
    }, []);

    if (state.status === "failed") {
      const message = state.cause instanceof Error ? state.cause.message : "chunk failed to load";
      return (
        <div
          role="alert"
          style={{ alignItems: "center", display: "flex", flexDirection: "column", gap: "0.75rem", padding: "3rem 1rem" }}
        >
          <p style={{ margin: 0 }}>This page failed to load: {message}</p>
          <button type="button" onClick={retry}>
            Retry
          </button>
        </div>
      );
    }
    if (state.status === "pending") {
      return <>{fallback ?? <DefaultPendingFallback />}</>;
    }

    const Content = state.component as ComponentType<Record<string, unknown>>;
    return (
      <>
        <Content {...props} />
      </>
    );
  };
};
