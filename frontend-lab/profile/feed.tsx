/**
 * The lab feed, free of browser globals so the same tree renders on the
 * client (profile/main.tsx) and at build time (profile/entry-server.tsx).
 * Options arrive as props parsed from the URL by each entry.
 *
 *   ?first=3   prerender (and hydrate) only the first 3 sections; the rest
 *              render after hydration, a few per frame (useFirstScreenCount)
 */
import { useEffect } from "react";
import { useFirstScreenCount } from "@ovasabi/frontend-kit";
import { MinimalCard, MinimalCullSection, MinimalGlobalStyles, MinimalSkeleton, MinimalStack, MinimalThemeProvider } from "@ovasabi/ui-minimal";

export interface FeedOptions {
  sections: number;
  cull: boolean;
  /** Sections in the first screen; equal to `sections` renders everything up front. */
  first: number;
}

declare global {
  interface Window {
    /** performance.now() when the feed first committed (hydration or client render). */
    __labMountedAt?: number;
    /** performance.now() when every section had committed. */
    __labFeedComplete?: number;
  }
}

export const feedOptions = (search: string): FeedOptions => {
  const params = new URLSearchParams(search);
  const sections = Math.max(1, Number(params.get("sections") ?? 80));
  const first = Math.min(sections, Math.max(1, Number(params.get("first") ?? sections)));
  return { sections, cull: params.get("cull") === "1", first };
};

const SectionBody = ({ index }: { index: number }) => (
  <MinimalStack rhythm="sm">
    {[0, 1, 2].map((card) => (
      <MinimalCard key={card}>
        <h3 style={{ margin: 0 }}>
          Section {index} · item {card + 1}
        </h3>
        <p style={{ margin: "8px 0" }}>
          A feed card with a heading, a paragraph of body text, and a loading placeholder, the shape most product
          screens repeat many times.
        </p>
        <MinimalSkeleton />
      </MinimalCard>
    ))}
  </MinimalStack>
);

export const Feed = ({ sections, cull, first }: FeedOptions) => {
  const count = useFirstScreenCount(first, sections);
  useEffect(() => {
    window.__labMountedAt ??= performance.now();
  }, []);
  useEffect(() => {
    if (count === sections) window.__labFeedComplete ??= performance.now();
  }, [count, sections]);
  return (
    <MinimalThemeProvider>
      <MinimalGlobalStyles />
      <main style={{ maxWidth: 560, margin: "0 auto", padding: 16 }}>
        <MinimalStack rhythm="md">
          {Array.from({ length: count }, (_, index) =>
            cull ? (
              <MinimalCullSection key={index} estimatedSize="560px">
                <SectionBody index={index} />
              </MinimalCullSection>
            ) : (
              <section key={index}>
                <SectionBody index={index} />
              </section>
            ),
          )}
        </MinimalStack>
      </main>
    </MinimalThemeProvider>
  );
};
