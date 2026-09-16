import { styled } from "@linaria/react";
import { css } from "@linaria/core";
import { minimalVars } from "@ovasabi/ui-minimal/tokens";

/*
 * Linaria spike (research doc section 8, option C): a MinimalCard-shaped
 * component whose styles are extracted at build time. It reads the same
 * `minimalVars` tokens ui-minimal uses, which wyw-in-js must evaluate from
 * Foundation's TypeScript source at build time — the question this spike
 * answers before any real primitive moves.
 */

const enter = css`
  @keyframes minimal-fade-in {
    from {
      opacity: 0;
    }
  }
  animation: minimal-fade-in ${minimalVars.motion.standard} ${minimalVars.motion.easeEntrance} backwards;
`;

export const LinariaCard = styled.section<{ padding: "sm" | "md" }>`
  background: ${minimalVars.color.bgSurface};
  border: 1px solid ${minimalVars.color.borderSubtle};
  box-shadow: ${minimalVars.shadow.subtle};
  border-radius: ${minimalVars.radius.md};
  display: grid;
  gap: ${minimalVars.space.xs};
  padding: ${(props) => (props.padding === "sm" ? "12px" : "16px")};

  &[data-minimal-variant="muted"] {
    background: ${minimalVars.color.bgSurfaceAlt};
  }

  @media (prefers-reduced-motion: reduce) {
    animation: none;
  }
`;

export const enterClass = enter;
