import { useEffect, useState } from "react";

/**
 * Keep an element mounted long enough for its close transition.
 *
 * The replacement for framer-motion's `AnimatePresence` where an exit actually
 * matters (research doc section 14). It is deliberately small: no measurement,
 * no per-frame work. Opening mounts the element in its `"closed"` state and
 * flips it to `"open"` two frames later, so a CSS transition runs from one to
 * the other; closing flips it back and unmounts after `exitMs`.
 *
 * The first render honours the initial value without animating, which is what
 * `AnimatePresence initial={false}` did. Works in every engine: the CSS decides
 * whether anything moves, and an engine that cannot interpolate the property
 * simply changes state.
 */
export const useMinimalPresence = (open: boolean, exitMs: number) => {
  const [mounted, setMounted] = useState(open);
  const [shown, setShown] = useState(open);

  useEffect(() => {
    if (open) {
      setMounted(true);
      let inner = 0;
      const outer = requestAnimationFrame(() => {
        inner = requestAnimationFrame(() => setShown(true));
      });
      return () => {
        cancelAnimationFrame(outer);
        cancelAnimationFrame(inner);
      };
    }
    setShown(false);
    const timer = setTimeout(() => setMounted(false), exitMs);
    return () => clearTimeout(timer);
  }, [open, exitMs]);

  return {
    mounted: open || mounted,
    state: open && shown ? ("open" as const) : ("closed" as const),
  };
};
