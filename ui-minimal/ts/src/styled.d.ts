import "styled-components";

import type { MinimalTheme } from "./types";

declare module "styled-components" {
  export interface DefaultTheme extends MinimalTheme {}
}
