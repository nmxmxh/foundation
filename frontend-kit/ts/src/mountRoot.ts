import { createElement, startTransition, useEffect, useState, type ReactNode } from "react";
import { createRoot, hydrateRoot, type Root } from "react-dom/client";

/** True from a hydrating mountRoot until the hydrated tree's effects have run. */
let hydrating = false;

/** Renders nothing of its own, so the server markup (rendered without it) still matches. */
function HydrationBoundary({ children }: { children: ReactNode }) {
  useEffect(() => {
    hydrating = false;
  }, []);
  return children;
}

/**
 * Mount the app into `container`, hydrating markup the build prerendered
 * (see `prerenderShell` in @ovasabi/frontend-kit/vite) and client-rendering
 * when the container is empty. Hydration adopts the painted DOM instead of
 * replacing it, so nothing the user already sees is torn down and rebuilt.
 */
export function mountRoot(container: Element, element: ReactNode): Root {
  if (container.hasChildNodes()) {
    hydrating = true;
    return hydrateRoot(container, createElement(HydrationBoundary, null, element));
  }
  const root = createRoot(container);
  root.render(element);
  return root;
}

/**
 * Prerender only the first screen of a list. Returns `first` while rendering
 * at build time and while hydrating that markup, then grows by `step` per
 * frame until `total`; on a client-rendered mount it returns `total` at once.
 *
 *   const count = useFirstScreenCount(3, sections.length) // 3 in the HTML, the rest after hydration
 *
 * Keeps the HTML, its parse and the hydration pass proportional to what the
 * viewport shows. The rest renders below the fold in an interruptible
 * transition, so it adds no long task and moves nothing already on screen.
 */
export function useFirstScreenCount(first: number, total: number, step = first): number {
  const [count, setCount] = useState(() => (typeof document === "undefined" || hydrating ? Math.min(first, total) : total));
  useEffect(() => {
    if (count >= total) return;
    // One step per frame, each its own small commit: a single commit of the
    // whole remainder is one long task on a weak phone (73 ms at CPU 6x in the
    // lab for 37 sections), transition or not — transitions yield during
    // render, never during commit.
    const frame = requestAnimationFrame(() =>
      startTransition(() => setCount((current) => Math.min(total, current + Math.max(1, step)))),
    );
    return () => cancelAnimationFrame(frame);
  }, [count, total, step]);
  return Math.min(count, total);
}

/**
 * The general form of useFirstScreenCount for any value: `firstScreen` at build
 * time and while hydrating, `full` in one transition after. Prefer
 * useFirstScreenCount for lists, which grows in per-frame steps.
 */
export function useFirstScreen<T>(firstScreen: T, full: T): T {
  const [value, setValue] = useState<T>(() => (typeof document === "undefined" || hydrating ? firstScreen : full));
  useEffect(() => {
    startTransition(() => setValue(full));
  }, [full]);
  return value;
}
