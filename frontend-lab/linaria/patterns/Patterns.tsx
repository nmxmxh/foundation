import { createElement } from "react";
import { styled } from "@linaria/react";
import { css } from "@linaria/core";
import { minimalVars, until } from "@ovasabi/ui-minimal/tokens";

// 1. A fragment as a plain string (styled-components `css` fragments become these).
const focusRing = `
  &:focus-visible {
    outline: 2px solid ${minimalVars.color.borderFocus};
    outline-offset: 2px;
  }
`;
// 2. Variant rules as build-time strings.
const variantRules = (name: string, values: readonly string[], block: (v: string) => string) =>
  values.map((v) => `&:where([data-minimal-${name}="${v}"]) { ${block(v)} }`).join("\n");

// 3. Global keyframes declared once, referenced by name.
export const globals = css`
  :global() {
    @keyframes minimal-enter-pop {
      from {
        opacity: 0;
        scale: 0.97;
      }
    }
  }
`;

export const Button = styled.button<{ $size: "sm" | "md"; $full: boolean }>`
  ${focusRing}
  display: inline-flex;
  padding: ${(p) => (p.$size === "sm" ? "6px 10px" : "10px 14px")};
  width: ${(p) => (p.$full ? "100%" : "auto")};
  animation: minimal-enter-pop 0.3s backwards;
  ${variantRules("tone", ["brand", "danger"], (tone) => `background: ${tone === "brand" ? minimalVars.color.brand : "red"};`)}
  ${until("page")} {
    padding: ${(p) => (p.$size === "sm" ? "4px 8px" : "8px 12px")};
  }
`;

// 4. styled of styled, and a component selector.
export const Quiet = styled(Button)`
  background: transparent;
`;
export const Row = styled.div`
  ${Button} + ${Button} {
    margin-left: 8px;
  }
`;

export const render = () =>
  createElement(Row, null, createElement(Button, { $size: "sm", $full: false, "data-minimal-tone": "brand" }, "a"), createElement(Quiet, { $size: "md", $full: true, as: "a", href: "#" }, "b"));
