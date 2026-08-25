import { createContext, useContext, useMemo, type PropsWithChildren } from "react";

/**
 * The render lane and frame budget a surface is running under.
 *
 * This contract was published by `ui-minimal`, removed from the package, and
 * then re-implemented by hand in consuming projects — each copy free to drift
 * from the others. It is restored here so there is one definition again. The
 * names are deliberately the originals (`ReactStyle*`, not `Minimal*`) so a
 * project can delete its local copy and change only the import path.
 *
 * A component reads its budget; it does not decide it. The lane is set once, at
 * the entry point that knows whether it is server-rendering, hydrating, or
 * running inside a worker.
 */
export type ReactStyleRuntimeLane = "ssr" | "csr" | "worker";

/** How much motion and interactivity the surface is expected to sustain. */
export type ReactStyleQuality = "static" | "interactive" | "animated" | "realtime";

export interface ReactStyleRuntime {
  lane: ReactStyleRuntimeLane;
  quality: ReactStyleQuality;
  /** Target frame time. 16.7 is 60fps. */
  frameBudgetMs: number;
  /** The frame time above which a stall counts as a user-visible hitch. */
  hitchBudgetMs: number;
  reducedMotion?: boolean;
}

export interface ReactStyleConfig {
  name: string;
  runtime: ReactStyleRuntime;
}

export const minimalReactStyle: ReactStyleConfig = {
  name: "ovasabi-minimal-react-style",
  runtime: {
    lane: "ssr",
    quality: "interactive",
    frameBudgetMs: 16.7,
    hitchBudgetMs: 50,
  },
};

/**
 * Builds a config for one lane, defaulting everything else. Entry points differ only
 * in their lane, so making that the positional argument keeps the call site
 * short: `createReactStyle("csr")`.
 */
export const createReactStyle = (
  lane: ReactStyleRuntimeLane,
  overrides?: Partial<Omit<ReactStyleRuntime, "lane">> & { name?: string },
): ReactStyleConfig => {
  const { name, ...runtime } = overrides ?? {};
  return {
    name: name ?? minimalReactStyle.name,
    runtime: { ...minimalReactStyle.runtime, ...runtime, lane },
  };
};

const ReactStyleContext = createContext<ReactStyleConfig>(minimalReactStyle);

export interface ReactStyleProviderProps {
  value: ReactStyleConfig;
}

export const ReactStyleProvider = ({ value, children }: PropsWithChildren<ReactStyleProviderProps>) => {
  const memoized = useMemo(() => value, [value]);
  return <ReactStyleContext.Provider value={memoized}>{children}</ReactStyleContext.Provider>;
};

/** Returns the active runtime style, falling back to the package default. */
export const useReactStyle = (): ReactStyleConfig => useContext(ReactStyleContext);
