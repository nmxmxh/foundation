import "styled-components";

import type { ResolvedMinimalTheme } from "./types";

declare module "styled-components" {
  /*
   * Resolved, not `MinimalTheme`.
   *
   * `space`, `breakpoint`, `control` and `overlay` are optional on the authoring
   * interface so an application's own theme literal — written before any of
   * them existed — still satisfies it. What actually reaches styled-components
   * is never that literal: `MinimalThemeProvider` runs it through
   * `createMinimalTheme` first, which fills in every one of them. Declaring the
   * authoring shape here made styled templates treat guaranteed tokens as
   * possibly-undefined, so the package that defines the scale could not read
   * its own newest steps without a non-null assertion at every call site.
   */
  export interface DefaultTheme extends ResolvedMinimalTheme {}
}
