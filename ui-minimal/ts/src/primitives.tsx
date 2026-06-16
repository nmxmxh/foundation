import {
  AnimatePresence,
  HTMLMotionProps,
  motion,
  type MotionValue,
  type Transition,
  useScroll,
  useSpring,
  useTransform,
  useVelocity,
} from "framer-motion";
import React, {
  ForwardedRef,
  HTMLAttributes,
  InputHTMLAttributes,
  Key,
  ReactNode,
  RefObject,
  CSSProperties,
  useCallback,
  forwardRef,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";
import { css, keyframes, styled } from "styled-components";

import { useMinimalMotion } from "./motion";
import { useMinimalTheme } from "./theme";
import type { MinimalDensity, MinimalEmphasis, MinimalSize, MinimalTheme, MinimalTone } from "./types";

type SurfaceVariant = "default" | "muted" | "raised" | "outlined";
type HeaderAlign = "start" | "center";
type ActionAlign = "start" | "center" | "end" | "between";
type InputState = "default" | "invalid" | "locked";
type ButtonVariant = "primary" | "secondary" | "ghost" | "quiet";
type TooltipPlacement = "top" | "bottom";
type FloatingPlacement = "top" | "bottom";
type MinimalScrollBehavior = "auto" | "smooth";
type LandingAnchor =
  | "center"
  | "top-left"
  | "bottom-left"
  | "bottom-right"
  | "left-visual"
  | "right-visual"
  | "offset"
  | "stacked";
type LandingVisualMode = "inline" | "background" | "side" | "canvas" | "none";
type LandingIntensity = "calm" | "standard" | "statement";
type InfoLayout = "row" | "stack" | "split";

export interface MinimalOption<T extends string> {
  value: T;
  label: ReactNode;
  description?: ReactNode;
  disabled?: boolean;
  meta?: ReactNode;
  searchableText?: string;
}

export interface MinimalHeaderProps extends Omit<HTMLMotionProps<"header">, "children" | "ref" | "title"> {
  children?: ReactNode;
  kicker?: ReactNode;
  title: ReactNode;
  subtitle?: ReactNode;
  description?: ReactNode;
  meta?: ReactNode;
  actions?: ReactNode;
  align?: HeaderAlign;
  titleAs?: "h1" | "h2" | "h3" | "h4";
}

export interface MinimalButtonProps extends Omit<HTMLMotionProps<"button">, "children" | "ref"> {
  children?: ReactNode;
  variant?: ButtonVariant;
  tone?: MinimalTone;
  size?: MinimalSize;
  fullWidth?: boolean;
  loading?: boolean;
  leading?: ReactNode;
  trailing?: ReactNode;
}

export interface MinimalCardProps extends Omit<HTMLMotionProps<"section">, "children" | "ref"> {
  children?: ReactNode;
  header?: ReactNode;
  footer?: ReactNode;
  variant?: SurfaceVariant;
  padding?: MinimalSize;
  hoverable?: boolean;
}

export interface MinimalInputProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "prefix" | "size"> {
  label?: ReactNode;
  description?: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  prefix?: ReactNode;
  suffix?: ReactNode;
  inputSize?: MinimalSize;
  locked?: boolean;
  containerClassName?: string;
}

export interface MinimalDropdownProps<T extends string> extends Omit<HTMLAttributes<HTMLDivElement>, "onChange"> {
  options: readonly MinimalOption<T>[];
  value?: T;
  onChange: (next: T) => void;
  label?: ReactNode;
  placeholder?: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  searchable?: boolean;
  searchPlaceholder?: string;
  disabled?: boolean;
  panelMaxHeight?: number;
  panelMinWidth?: number;
  matchTriggerWidth?: boolean;
  renderValue?: (option: MinimalOption<T> | undefined) => ReactNode;
}

export interface MinimalBadgeProps extends HTMLAttributes<HTMLSpanElement> {
  children: ReactNode;
  tone?: MinimalTone;
  emphasis?: MinimalEmphasis;
  size?: MinimalSize;
  icon?: ReactNode;
}

export interface MinimalAlertProps extends Omit<HTMLAttributes<HTMLElement>, "title"> {
  children: ReactNode;
  tone?: Exclude<MinimalTone, "brand" | "neutral">;
  title?: ReactNode;
  icon?: ReactNode;
  action?: ReactNode;
}

export interface MinimalEmptyStateProps extends Omit<HTMLAttributes<HTMLElement>, "title"> {
  title: ReactNode;
  description: ReactNode;
  eyebrow?: ReactNode;
  icon?: ReactNode;
  action?: ReactNode;
  align?: HeaderAlign;
}

export interface MinimalFilterBarProps<T extends string> extends Omit<HTMLAttributes<HTMLElement>, "onChange"> {
  value: T;
  options: readonly MinimalOption<T>[];
  onChange: (next: T) => void;
  ariaLabel: string;
  size?: MinimalSize;
  leading?: ReactNode;
  trailing?: ReactNode;
}

export interface MinimalSegmentedControlProps<T extends string> extends Omit<HTMLAttributes<HTMLDivElement>, "onChange"> {
  value: T;
  options: readonly MinimalOption<T>[];
  onChange: (next: T) => void;
  ariaLabel: string;
  size?: MinimalSize;
  disabled?: boolean;
}

export interface MinimalExplainerProps extends Omit<HTMLAttributes<HTMLDivElement>, "title"> {
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
  icon?: ReactNode;
  action?: ReactNode;
  defaultOpen?: boolean;
  open?: boolean;
  onOpenChange?: (next: boolean) => void;
}

export interface MinimalStatCardProps extends Omit<HTMLAttributes<HTMLElement>, "title"> {
  label: ReactNode;
  value: ReactNode;
  title?: ReactNode;
  hint?: ReactNode;
  trend?: ReactNode;
  icon?: ReactNode;
  footer?: ReactNode;
  tone?: MinimalTone;
}

export interface MinimalFormSectionProps extends Omit<HTMLAttributes<HTMLElement>, "title"> {
  title: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
  children: ReactNode;
}

export interface MinimalFieldGridProps extends HTMLAttributes<HTMLDivElement> {
  children: ReactNode;
  columns?: 1 | 2 | 3;
}

export interface MinimalActionRowProps extends HTMLAttributes<HTMLDivElement> {
  children: ReactNode;
  align?: ActionAlign;
}

export interface MinimalTableColumn<T> {
  id: string;
  header: ReactNode;
  cell: (row: T, rowIndex: number) => ReactNode;
  align?: "left" | "center" | "right";
  width?: string;
  headerDescription?: ReactNode;
}

export interface MinimalTableProps<T> extends HTMLAttributes<HTMLDivElement> {
  rows: readonly T[];
  columns: readonly MinimalTableColumn<T>[];
  rowKey?: (row: T, rowIndex: number) => Key;
  caption?: ReactNode;
  emptyState?: ReactNode;
  density?: MinimalDensity;
  onRowClick?: (row: T, rowIndex: number) => void;
}

export interface MinimalCalendarProps extends Omit<HTMLAttributes<HTMLElement>, "onChange"> {
  value?: Date | string | null;
  onChange?: (next: Date) => void;
  month?: Date | string | null;
  onMonthChange?: (next: Date) => void;
  weekStartsOn?: 0 | 1;
  locale?: string;
  renderDayContent?: (date: Date, selected: boolean, inCurrentMonth: boolean) => ReactNode;
}

export interface MinimalTooltipProps {
  content: ReactNode;
  children: ReactNode;
  placement?: TooltipPlacement;
  openDelay?: number;
  disabled?: boolean;
  maxWidth?: string;
}

export interface MinimalActionModalProps {
  open: boolean;
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  tone?: Exclude<MinimalTone, "neutral"> | "neutral";
  confirmLabel?: string;
  cancelLabel?: string;
  confirmDisabled?: boolean;
  maxWidth?: string;
  maxHeight?: string;
  align?: HeaderAlign;
  bodyScrollable?: boolean;
  mobileSheet?: boolean;
  onClose: () => void;
  onConfirm?: () => void | Promise<void>;
}

export interface MinimalDisplaySectionProps extends Omit<HTMLMotionProps<"section">, "children" | "ref" | "title"> {
  eyebrow?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  visual?: ReactNode;
  anchor?: LandingAnchor;
  visualMode?: LandingVisualMode;
  intensity?: LandingIntensity;
  minHeight?: string;
  mediaAspectRatio?: string;
  backgroundImage?: string;
  overlay?: string;
  children?: ReactNode;
}

export interface MinimalLandingSectionProps extends Omit<HTMLAttributes<HTMLElement>, "title"> {
  eyebrow?: ReactNode;
  title?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
  anchor?: LandingAnchor;
  intensity?: LandingIntensity;
  media?: ReactNode;
  mediaAspectRatio?: string;
}

export interface MinimalInfoPanelProps extends Omit<HTMLAttributes<HTMLElement>, "title"> {
  eyebrow?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  icon?: ReactNode;
  meta?: ReactNode;
  action?: ReactNode;
  tone?: MinimalTone;
  layout?: InfoLayout;
}

export interface MinimalSkeletonProps extends HTMLAttributes<HTMLSpanElement> {
  width?: string;
  height?: string;
  inline?: boolean;
  radius?: string;
}

export interface MinimalSkipLinkProps extends HTMLAttributes<HTMLAnchorElement> {
  href?: string;
  children?: ReactNode;
}

export interface MinimalAppShellProps extends HTMLAttributes<HTMLDivElement> {
  children: ReactNode;
  sidebar?: ReactNode;
  mobileNavigation?: ReactNode;
  systemLayer?: ReactNode;
  sidebarWidth?: string;
  bannerOffset?: string;
  mobile?: boolean;
}

export interface MinimalSidebarProps extends HTMLAttributes<HTMLElement> {
  children: ReactNode;
  mainRef?: RefObject<HTMLElement | null>;
  width?: string;
  bannerOffset?: string;
}

export interface MinimalScrollMainProps extends Omit<HTMLMotionProps<"main">, "children" | "ref"> {
  children: ReactNode;
  sidebarWidth?: string;
  bannerOffset?: string;
  mobile?: boolean;
  compact?: boolean;
  scrollAttribute?: string;
}

export interface MinimalScrollFeedbackOptions {
  enabled?: boolean;
  maxSkew?: number;
  minScale?: number;
}

export interface MinimalScrollFeedback {
  skewY: MotionValue<number> | number;
  scale: MotionValue<number> | number;
}

export interface MinimalScrollFeedbackSurfaceProps extends Omit<HTMLMotionProps<"div">, "children" | "ref"> {
  children: ReactNode;
  feedback?: MinimalScrollFeedback;
}

type FloatingPosition = {
  top: number;
  left: number;
  width: number;
  maxHeight: number;
  placement: FloatingPlacement;
};

const densityPadding = {
  compact: "10px 12px",
  comfortable: "14px 16px",
  relaxed: "16px 18px",
} satisfies Record<MinimalDensity, string>;

const sizePadding = {
  sm: "6px 12px",
  md: "7px 16px",
  lg: "10px 18px",
} satisfies Record<MinimalSize, string>;

const sizeFont = {
  sm: "0.6875rem",
  md: "0.8125rem",
  lg: "0.875rem",
} satisfies Record<MinimalSize, string>;

const cardPadding = {
  sm: "16px",
  md: "20px",
  lg: "24px",
} satisfies Record<MinimalSize, string>;

const buttonMinHeight = {
  sm: "32px",
  md: "34px",
  lg: "40px",
} satisfies Record<MinimalSize, string>;

const inputPadding = {
  sm: "8px 12px",
  md: "12px 16px",
  lg: "14px 16px",
} satisfies Record<MinimalSize, string>;

const toneAccent = (theme: MinimalTheme, tone: MinimalTone) => {
  switch (tone) {
    case "brand":
      return { color: theme.color.brand, soft: theme.color.brandSoft };
    case "info":
      return { color: theme.color.info, soft: theme.color.infoSoft };
    case "success":
      return { color: theme.color.success, soft: theme.color.successSoft };
    case "warning":
      return { color: theme.color.warning, soft: theme.color.warningSoft };
    case "danger":
      return { color: theme.color.danger, soft: theme.color.dangerSoft };
    case "neutral":
    default:
      return { color: theme.color.textSecondary, soft: theme.color.bgSurfaceAlt };
  }
};

const tonePresentation = (theme: MinimalTheme, tone: MinimalTone, emphasis: MinimalEmphasis) => {
  const accent = toneAccent(theme, tone);
  switch (emphasis) {
    case "solid":
      return {
        background: accent.color,
        color: theme.color.textInverse,
        border: accent.color,
      };
    case "outline":
      return {
        background: "transparent",
        color: accent.color,
        border: accent.color,
      };
    case "soft":
    default:
      return {
        background: accent.soft,
        color: accent.color,
        border: accent.soft,
      };
  }
};

const surfacePresentation = (theme: MinimalTheme, variant: SurfaceVariant) => {
  switch (variant) {
    case "muted":
      return {
        background: theme.color.bgSurfaceAlt,
        border: theme.color.borderSubtle,
        shadow: "none",
      };
    case "raised":
      return {
        background: theme.color.bgElevated,
        border: theme.color.borderSubtle,
        shadow: theme.shadow.medium,
      };
    case "outlined":
      return {
        background: "transparent",
        border: theme.color.borderStrong,
        shadow: "none",
      };
    case "default":
    default:
      return {
        background: theme.color.bgSurface,
        border: theme.color.borderSubtle,
        shadow: theme.shadow.subtle,
      };
  }
};

const actionJustify = {
  start: "flex-start",
  center: "center",
  end: "flex-end",
  between: "space-between",
} satisfies Record<ActionAlign, string>;

const floatingYOffset = 8;
const enterCurve = "cubic-bezier(0.22, 1, 0.36, 1)";
const moveCurve = "cubic-bezier(0.25, 1, 0.5, 1)";
// Press settles fast and flat — a mechanical click, not a bounce. Kept separate
// from enter/move so a lift can be soft while the press stays crisp.
const pressCurve = "cubic-bezier(0.2, 0, 0, 1)";

// tonalShadow casts a short, soft shadow tinted to the element's own accent
// rather than a generic grey. The negative spread keeps it close to the edge so
// a 1px lift reads as the surface catching light, not floating away — the single
// biggest tell of hand-built vs. default UI, so it lives in one place.
const tonalShadow = (accent: string, strength = 52, y = 10, blur = 22, spread = -12) =>
  `0 ${y}px ${blur}px ${spread}px color-mix(in srgb, ${accent} ${strength}%, transparent)`;

// litEdge is a hairline highlight along the top of a filled surface so the fill
// reads as a lit material with a real top edge, the way a physical control does.
const litEdge = (strength: number) =>
  `inset 0 1px 0 color-mix(in srgb, #ffffff ${strength}%, transparent)`;
export const minimalMainScrollAttribute = "data-minimal-main-scroll";
const skeletonSweep = keyframes`
  0% {
    background-position: 100% 50%;
  }

  100% {
    background-position: 0 50%;
  }
`;

const focusRing = css`
  &:focus-visible {
    outline: 2px solid ${({ theme }) => theme.color.borderFocus};
    outline-offset: 2px;
  }
`;

const clickableReset = css`
  appearance: none;
  border: 0;
  background: transparent;
  font: inherit;
`;

const Style = {
  HeaderShell: styled(motion.header)<{ $align: HeaderAlign }>`
    display: grid;
    gap: ${({ theme }) => theme.spacing.sm};
    justify-items: ${({ $align }) => ($align === "center" ? "center" : "stretch")};
    text-align: ${({ $align }) => ($align === "center" ? "center" : "left")};
  `,
  HeaderTop: styled.div`
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: ${({ theme }) => theme.spacing.md};
    width: 100%;
  `,
  HeaderCopy: styled.div`
    display: grid;
    gap: ${({ theme }) => theme.spacing.xs};
    min-width: 0;
  `,
  HeaderKicker: styled.p`
    margin: 0;
    color: ${({ theme }) => theme.color.textTertiary};
    font-size: ${({ theme }) => theme.typography.metaSize};
    font-weight: ${({ theme }) => theme.typography.weightSemibold};
    letter-spacing: 0.14em;
    text-transform: uppercase;
  `,
  HeaderTitle: styled.h1`
    margin: 0;
    color: ${({ theme }) => theme.color.textPrimary};
    font-family: ${({ theme }) => theme.typography.displayFamily};
    font-size: ${({ theme }) => theme.typography.displaySize};
    line-height: ${({ theme }) => theme.typography.lineHeightTight};
    letter-spacing: 0;
  `,
  HeaderSubtitle: styled.p`
    margin: 0;
    color: ${({ theme }) => theme.color.textSecondary};
    font-size: ${({ theme }) => theme.typography.bodySize};
    line-height: ${({ theme }) => theme.typography.lineHeightBody};
  `,
  HeaderMeta: styled.div`
    color: ${({ theme }) => theme.color.textTertiary};
    font-size: ${({ theme }) => theme.typography.captionSize};
  `,
  ButtonShell: styled(motion.button)<{
    $variant: ButtonVariant;
    $tone: MinimalTone;
    $size: MinimalSize;
    $fullWidth: boolean;
  }>`
    ${clickableReset}
    ${focusRing}
    ${({ theme, $tone, $variant }) => {
      const accent = toneAccent(theme, $tone);
      if ($variant === "secondary") {
        return css`
          background: transparent;
          color: ${accent.color};
          border: 1px solid ${accent.color};
        `;
      }
      if ($variant === "ghost") {
        return css`
          background: ${accent.soft};
          color: ${accent.color};
          border: 1px solid transparent;
        `;
      }
      if ($variant === "quiet") {
        return css`
          background: transparent;
          color: ${theme.color.textSecondary};
          border: 1px solid transparent;
        `;
      }
      return css`
        background: ${accent.color};
        color: ${theme.color.textInverse};
        border: 1px solid ${accent.color};
      `;
    }}
    align-items: center;
    border-radius: ${({ theme }) => theme.radius.sm};
    cursor: pointer;
    display: inline-flex;
    gap: ${({ theme }) => theme.spacing.sm};
    justify-content: center;
    letter-spacing: 0.02em;
    line-height: 1;
    min-height: ${({ $size }) => buttonMinHeight[$size]};
    padding: ${({ $size }) => sizePadding[$size]};
    /* Per-property timing: the lift eases out slowly, colour moves a touch
       faster, and the press transform is its own quick curve. Uniform timing is
       what makes default transitions feel mechanical-but-dead. */
    transition:
      background-color 220ms ${moveCurve},
      border-color 220ms ${moveCurve},
      box-shadow 260ms ${enterCurve},
      color 200ms ${moveCurve},
      transform 180ms ${pressCurve};
    width: ${({ $fullWidth }) => ($fullWidth ? "100%" : "auto")};
    font-size: ${({ $size }) => sizeFont[$size]};
    font-weight: ${({ theme }) => theme.typography.weightSemibold};

    /* Filled buttons rest with a hairline lit top edge so the fill is a material,
       not a swatch. Quiet/ghost buttons stay flat until touched. */
    ${({ $variant }) =>
      $variant === "primary"
        ? css`
            box-shadow: ${litEdge(14)};
          `
        : null}

    @media (hover: hover) and (pointer: fine) {
      &:not(:disabled):hover {
        transform: translateY(-1px);
        /* The shadow is tinted to the button's own tone and cast short + soft,
           so the lift feels owned by the colour instead of a generic float. */
        box-shadow: ${({ theme, $tone, $variant }) =>
          $variant === "quiet"
            ? "none"
            : $variant === "primary"
              ? `${litEdge(18)}, ${tonalShadow(toneAccent(theme, $tone).color, 52)}`
              : tonalShadow(toneAccent(theme, $tone).color, 30)};
      }
    }

    /* Active is a real press: it settles back onto the surface with a tighter,
       quicker shadow rather than fading out. */
    &:not(:disabled):active {
      transform: translateY(0);
      transition-duration: 70ms;
      box-shadow: ${({ theme, $tone, $variant }) =>
        $variant === "quiet"
          ? "none"
          : $variant === "primary"
            ? `${litEdge(8)}, ${tonalShadow(toneAccent(theme, $tone).color, 46, 3, 8, -6)}`
            : tonalShadow(toneAccent(theme, $tone).color, 26, 3, 8, -6)};
    }

    &:disabled {
      cursor: not-allowed;
      opacity: 0.56;
      transform: none;
      box-shadow: none;
    }

    /* No motion, no lie: drop the transform but keep colour/shadow feedback. */
    @media (prefers-reduced-motion: reduce) {
      transition:
        background-color 220ms ${moveCurve},
        border-color 220ms ${moveCurve},
        box-shadow 200ms ${enterCurve},
        color 200ms ${moveCurve};

      &:hover,
      &:active {
        transform: none;
      }
    }
  `,
  Spinner: styled.span`
    width: 0.9rem;
    height: 0.9rem;
    border: 2px solid currentColor;
    border-bottom-color: transparent;
    border-radius: 50%;
    display: inline-block;
  `,
  CardShell: styled(motion.section)<{
    $variant: SurfaceVariant;
    $padding: MinimalSize;
    $hoverable: boolean;
  }>`
    ${({ theme, $variant }) => {
      const surface = surfacePresentation(theme, $variant);
      return css`
        background: ${surface.background};
        border: 1px solid ${surface.border};
        box-shadow: ${surface.shadow};
      `;
    }}
    border-radius: ${({ theme }) => theme.radius.md};
    display: grid;
    gap: ${({ theme }) => theme.spacing.sm};
    overflow: hidden;
    padding: ${({ $padding }) => cardPadding[$padding]};
    transition:
      box-shadow 220ms ${moveCurve},
      transform 220ms ${moveCurve},
      border-color 220ms ${enterCurve};

    ${({ $hoverable, theme }) =>
      $hoverable
        ? css`
            @media (hover: hover) and (pointer: fine) {
              &:hover {
                border-color: ${theme.color.borderStrong};
                box-shadow: ${theme.shadow.floating};
                transform: translateY(-1px);
              }

              /* Don't move the surface for reduced-motion users — deepen the
                 shadow and border so the affordance still reads. */
              @media (prefers-reduced-motion: reduce) {
                &:hover {
                  transform: none;
                }
              }
            }
          `
        : null}
  `,
  CardSlot: styled.div`
    min-width: 0;
  `,
  FieldShell: styled.div`
    display: grid;
    gap: ${({ theme }) => theme.spacing.xs};
    width: 100%;
  `,
  FieldLabel: styled.label`
    color: ${({ theme }) => theme.color.textPrimary};
    font-size: ${({ theme }) => theme.typography.captionSize};
    font-weight: ${({ theme }) => theme.typography.weightSemibold};
  `,
  FieldDescription: styled.div`
    color: ${({ theme }) => theme.color.textTertiary};
    font-size: ${({ theme }) => theme.typography.captionSize};
    line-height: ${({ theme }) => theme.typography.lineHeightBody};
  `,
  InputFrame: styled.div<{ $state: InputState; $size: MinimalSize }>`
    ${({ theme, $state }) => {
      const borderColor =
        $state === "invalid"
          ? theme.color.danger
          : $state === "locked"
            ? theme.color.borderStrong
            : theme.color.borderSubtle;

      const background =
        $state === "locked" ? theme.color.bgSurfaceAlt : theme.color.bgSurface;

      return css`
        background: ${background};
        border: 1px solid ${borderColor};
      `;
    }}
    ${focusRing}
    align-items: center;
    border-radius: ${({ theme }) => theme.radius.sm};
    display: flex;
    gap: ${({ theme }) => theme.spacing.sm};
    min-height: ${({ $size }) => ($size === "sm" ? "36px" : $size === "lg" ? "48px" : "44px")};
    padding: ${({ $size }) => inputPadding[$size]};
    transition:
      border-color 160ms ${enterCurve},
      box-shadow 160ms ${enterCurve},
      background 160ms ${enterCurve};

    &:focus-within {
      border-color: ${({ theme, $state }) => ($state === "invalid" ? theme.color.danger : theme.color.borderFocus)};
      box-shadow: 0 0 0 3px ${({ theme, $state }) => ($state === "invalid" ? theme.color.dangerSoft : theme.color.brandSoft)};
    }
  `,
  InputAdornment: styled.span`
    color: ${({ theme }) => theme.color.textSecondary};
    display: inline-flex;
    flex-shrink: 0;
    align-items: center;
  `,
  InputField: styled.input`
    background: transparent;
    border: 0;
    color: ${({ theme }) => theme.color.textPrimary};
    flex: 1;
    font: inherit;
    min-width: 0;
    outline: none;
    padding: 0;

    &:-webkit-autofill,
    &:-webkit-autofill:hover,
    &:-webkit-autofill:focus,
    &:-webkit-autofill:active {
      -webkit-text-fill-color: ${({ theme }) => theme.color.textPrimary} !important;
      caret-color: ${({ theme }) => theme.color.textPrimary};
      -webkit-box-shadow: 0 0 0 1000px ${({ theme }) => theme.color.bgSurface} inset !important;
      box-shadow: 0 0 0 1000px ${({ theme }) => theme.color.bgSurface} inset !important;
      transition: background-color 9999s ease-out 0s;
    }
  `,
  FieldMessage: styled.p<{ $tone: MinimalTone }>`
    margin: 0;
    color: ${({ theme, $tone }) => toneAccent(theme, $tone).color};
    font-size: ${({ theme }) => theme.typography.captionSize};
    line-height: ${({ theme }) => theme.typography.lineHeightBody};
  `,
  BadgeShell: styled.span<{
    $tone: MinimalTone;
    $emphasis: MinimalEmphasis;
    $size: MinimalSize;
  }>`
    ${({ theme, $tone, $emphasis }) => {
      const presentation = tonePresentation(theme, $tone, $emphasis);
      return css`
        background: ${presentation.background};
        color: ${presentation.color};
        border: 1px solid ${presentation.border};
      `;
    }}
    align-items: center;
    border-radius: ${({ theme }) => theme.radius.sm};
    display: inline-flex;
    gap: ${({ theme }) => theme.spacing.xs};
    justify-content: center;
    letter-spacing: 0.04em;
    line-height: 1;
    padding: ${({ $size }) => ($size === "sm" ? "3px 8px" : $size === "lg" ? "5px 10px" : "4px 9px")};
    text-transform: uppercase;
    white-space: nowrap;
    font-size: ${({ $size, theme }) =>
      $size === "sm" ? theme.typography.metaSize : theme.typography.captionSize};
    font-weight: ${({ theme }) => theme.typography.weightSemibold};
  `,
  AlertShell: styled.section<{ $tone: Exclude<MinimalTone, "brand" | "neutral"> }>`
    ${({ theme, $tone }) => {
      const presentation = tonePresentation(theme, $tone, "soft");
      return css`
        background: ${presentation.background};
        color: ${presentation.color};
        border: 1px solid ${presentation.border};
      `;
    }}
    border-left-width: 2px;
    border-radius: ${({ theme }) => theme.radius.sm};
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    gap: 9px;
    padding: 10px 12px;
    font-size: 0.75rem;
  `,
  AlertIcon: styled.div`
    display: inline-flex;
    align-items: flex-start;
    justify-content: center;
    padding-top: 2px;
  `,
  AlertBody: styled.div`
    display: grid;
    gap: ${({ theme }) => theme.spacing.xs};
  `,
  AlertTitle: styled.strong`
    font-size: ${({ theme }) => theme.typography.captionSize};
    text-transform: uppercase;
    letter-spacing: 0.06em;
  `,
  EmptyStateShell: styled.section<{ $align: HeaderAlign }>`
    align-items: ${({ $align }) => ($align === "center" ? "center" : "flex-start")};
    background: ${({ theme }) => theme.color.bgSurfaceAlt};
    border: 1px dashed ${({ theme }) => theme.color.borderStrong};
    border-radius: ${({ theme }) => theme.radius.md};
    display: grid;
    gap: ${({ theme }) => theme.spacing.sm};
    justify-items: ${({ $align }) => ($align === "center" ? "center" : "stretch")};
    padding: ${({ theme }) => theme.spacing.xl};
    text-align: ${({ $align }) => ($align === "center" ? "center" : "left")};
  `,
  EmptyIcon: styled.div`
    width: 42px;
    height: 42px;
    border-radius: 999px;
    border: 1px solid ${({ theme }) => theme.color.borderStrong};
    color: ${({ theme }) => theme.color.textSecondary};
    display: inline-flex;
    align-items: center;
    justify-content: center;
  `,
  EmptyStateTitle: styled.h3`
    margin: 0;
    color: ${({ theme }) => theme.color.textPrimary};
    font-family: ${({ theme }) => theme.typography.displayFamily};
    font-size: ${({ theme }) => theme.typography.h2Size};
    line-height: ${({ theme }) => theme.typography.lineHeightTight};
    letter-spacing: 0;
  `,
  FilterBarShell: styled.section`
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: ${({ theme }) => theme.spacing.sm};
  `,
  FilterChip: styled.button<{ $selected: boolean; $size: MinimalSize }>`
    ${clickableReset}
    ${focusRing}
    ${({ theme, $selected }) =>
      $selected
        ? css`
            background: ${theme.color.bgSurface};
            border: 1px solid ${theme.color.borderFocus};
            color: ${theme.color.textPrimary};
            box-shadow: ${theme.shadow.subtle};
          `
        : css`
            background: transparent;
            border: 1px solid transparent;
            color: ${theme.color.textSecondary};
          `}
    border-radius: ${({ theme }) => theme.radius.sm};
    cursor: pointer;
    font-size: ${({ $size }) => sizeFont[$size]};
    font-weight: ${({ theme }) => theme.typography.weightSemibold};
    letter-spacing: 0.02em;
    line-height: 1;
    min-height: ${({ $size }) => buttonMinHeight[$size]};
    opacity: ${({ $selected }) => ($selected ? 1 : 0.84)};
    padding: ${({ $size }) => sizePadding[$size]};
    text-transform: uppercase;
    transition:
      opacity 240ms ${moveCurve},
      background-color 240ms ${moveCurve},
      border-color 240ms ${moveCurve},
      color 240ms ${moveCurve},
      box-shadow 240ms ${moveCurve};

    @media (hover: hover) and (pointer: fine) {
      &:hover {
        opacity: 1;
      }
    }
  `,
  SegmentedShell: styled.div<{ $size: MinimalSize }>`
    position: relative;
    display: inline-flex;
    align-items: stretch;
    background: ${({ theme }) => theme.color.bgSurface};
    border: 1px solid ${({ theme }) => theme.color.borderSubtle};
    border-radius: ${({ theme }) => theme.radius.sm};
    gap: 4px;
    padding: 4px;
    min-height: ${({ $size }) => ($size === "sm" ? "34px" : $size === "lg" ? "44px" : "38px")};
  `,
  SegmentedIndicator: styled(motion.div)<{ $count: number; $index: number }>`
    position: absolute;
    top: 4px;
    bottom: 4px;
    left: ${({ $count, $index }) => `calc(${$index} * (100% / ${$count}) + 4px)`};
    width: ${({ $count }) => `calc((100% / ${$count}) - 8px)`};
    background: ${({ theme }) => theme.color.bgSurface};
    border: 1px solid ${({ theme }) => theme.color.borderStrong};
    border-radius: ${({ theme }) => theme.radius.sm};
    box-shadow: ${({ theme }) => theme.shadow.subtle};
  `,
  SegmentedButton: styled.button<{ $selected: boolean; $size: MinimalSize; $count: number }>`
    ${clickableReset}
    ${focusRing}
    color: ${({ theme, $selected }) => ($selected ? theme.color.textPrimary : theme.color.textSecondary)};
    cursor: pointer;
    position: relative;
    z-index: 1;
    min-width: 72px;
    width: ${({ $count }) => `${100 / $count}%`};
    padding: ${({ $size }) => ($size === "sm" ? "6px 10px" : $size === "lg" ? "10px 14px" : "8px 12px")};
    letter-spacing: 0;
    line-height: 1;
    font-size: ${({ $size }) => sizeFont[$size]};
    font-weight: ${({ theme }) => theme.typography.weightSemibold};

    &:disabled {
      cursor: not-allowed;
      opacity: 0.5;
    }
  `,
  ExplainerShell: styled.div`
    border: 1px solid ${({ theme }) => theme.color.borderSubtle};
    border-radius: ${({ theme }) => theme.radius.md};
    background: ${({ theme }) => theme.color.bgSurfaceAlt};
    overflow: hidden;
  `,
  ExplainerToggle: styled.button`
    ${clickableReset}
    ${focusRing}
    width: 100%;
    color: ${({ theme }) => theme.color.textPrimary};
    display: flex;
    align-items: center;
    gap: ${({ theme }) => theme.spacing.sm};
    justify-content: space-between;
    padding: ${({ theme }) => theme.spacing.md};
    cursor: pointer;
  `,
  ExplainerCopy: styled.div`
    display: flex;
    align-items: center;
    gap: ${({ theme }) => theme.spacing.sm};
    min-width: 0;
  `,
  ExplainerText: styled.div`
    display: grid;
    gap: ${({ theme }) => theme.spacing.xs};
    text-align: left;
  `,
  ExplainerActions: styled.div`
    display: flex;
    align-items: center;
    gap: ${({ theme }) => theme.spacing.sm};
  `,
  ExplainerPanel: styled(motion.div)`
    overflow: hidden;
  `,
  ExplainerPanelBody: styled.div`
    padding: 0 ${({ theme }) => theme.spacing.md} ${({ theme }) => theme.spacing.md};
  `,
  StatShell: styled.article<{ $tone: MinimalTone }>`
    ${({ theme, $tone }) => {
      const accent = toneAccent(theme, $tone);
      return css`
        background: ${theme.color.bgSurface};
        border: 1px solid ${theme.color.borderSubtle};
        box-shadow: ${theme.shadow.subtle};
        --minimal-stat-accent: ${accent.color};
        --minimal-stat-bg: ${accent.soft};
      `;
    }}
    border-radius: ${({ theme }) => theme.radius.md};
    display: grid;
    gap: 4px;
    min-height: 0;
    padding: 20px;
  `,
  StatMeta: styled.div`
    display: flex;
    align-items: center;
    gap: ${({ theme }) => theme.spacing.sm};
  `,
  StatIcon: styled.div`
    width: 36px;
    height: 36px;
    border-radius: ${({ theme }) => theme.radius.sm};
    background: var(--minimal-stat-bg);
    color: var(--minimal-stat-accent);
    display: inline-flex;
    align-items: center;
    justify-content: center;
  `,
  StatLabel: styled.span`
    color: ${({ theme }) => theme.color.textTertiary};
    font-size: ${({ theme }) => theme.typography.metaSize};
    font-weight: ${({ theme }) => theme.typography.weightSemibold};
    letter-spacing: 0.08em;
    text-transform: uppercase;
  `,
  StatValue: styled.strong`
    color: ${({ theme }) => theme.color.textPrimary};
    font-size: 1.625rem;
    font-weight: 300;
    line-height: ${({ theme }) => theme.typography.lineHeightTight};
    letter-spacing: 0;
    font-variant-numeric: tabular-nums;
  `,
  StatHint: styled.p`
    margin: 0;
    color: var(--minimal-stat-accent);
    font-size: ${({ theme }) => theme.typography.captionSize};
  `,
  StatTitle: styled.p`
    margin: 0;
    color: ${({ theme }) => theme.color.textSecondary};
    font-size: ${({ theme }) => theme.typography.bodySize};
    line-height: ${({ theme }) => theme.typography.lineHeightBody};
  `,
  FormSectionShell: styled.section`
    display: grid;
    gap: ${({ theme }) => theme.spacing.md};
  `,
  FormSectionHeader: styled.div`
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: ${({ theme }) => theme.spacing.md};
  `,
  FormSectionCopy: styled.div`
    display: grid;
    gap: ${({ theme }) => theme.spacing.xs};
  `,
  FormSectionTitle: styled.h3`
    margin: 0;
    color: ${({ theme }) => theme.color.textPrimary};
    font-family: ${({ theme }) => theme.typography.displayFamily};
    font-size: 1.125rem;
    line-height: ${({ theme }) => theme.typography.lineHeightTight};
    letter-spacing: 0;
  `,
  FieldGridShell: styled.div<{ $columns: 1 | 2 | 3 }>`
    display: grid;
    gap: ${({ theme }) => theme.spacing.md};
    grid-template-columns: repeat(${({ $columns }) => $columns}, minmax(0, 1fr));

    @media (max-width: 800px) {
      grid-template-columns: 1fr;
    }
  `,
  ActionRowShell: styled.div<{ $align: ActionAlign }>`
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: ${({ theme }) => theme.spacing.sm};
    justify-content: ${({ $align }) => actionJustify[$align]};
  `,
  TableShell: styled.div`
    overflow-x: auto;
    border-radius: ${({ theme }) => theme.radius.md};
    border: 1px solid ${({ theme }) => theme.color.borderSubtle};
    background: ${({ theme }) => theme.color.bgSurface};
  `,
  StyledTable: styled.table<{ $density: MinimalDensity }>`
    width: 100%;
    border-collapse: separate;
    border-spacing: 0;
    font-size: ${({ theme }) => theme.typography.captionSize};

    caption {
      caption-side: top;
      padding: ${({ theme }) => theme.spacing.md};
      text-align: left;
      color: ${({ theme }) => theme.color.textSecondary};
      font-size: ${({ theme }) => theme.typography.captionSize};
    }

    th,
    td {
      padding: ${({ $density }) => densityPadding[$density]};
      border-bottom: 1px solid ${({ theme }) => theme.color.borderSubtle};
      vertical-align: top;
    }

    tbody tr:last-child td {
      border-bottom: 0;
    }

    tbody tr[data-clickable="true"] {
      cursor: pointer;
    }

    @media (hover: hover) and (pointer: fine) {
      tbody tr[data-clickable="true"]:hover {
        background: ${({ theme }) => theme.color.bgSurfaceHover};
      }
    }
  `,
  TableHeaderCell: styled.th<{ $align: "left" | "center" | "right"; $width?: string }>`
    width: ${({ $width }) => $width ?? "auto"};
    text-align: ${({ $align }) => $align};
    background: ${({ theme }) => theme.color.bgSurface};
    color: ${({ theme }) => theme.color.textSecondary};
    font-family: ${({ theme }) => theme.typography.bodyFamily};
    font-size: ${({ theme }) => theme.typography.metaSize};
    font-weight: ${({ theme }) => theme.typography.weightSemibold};
    letter-spacing: 0.04em;
    text-transform: uppercase;
  `,
  TableCell: styled.td<{ $align: "left" | "center" | "right" }>`
    text-align: ${({ $align }) => $align};
  `,
  CalendarShell: styled.section`
    display: grid;
    gap: ${({ theme }) => theme.spacing.md};
    border: 1px solid ${({ theme }) => theme.color.borderSubtle};
    border-radius: ${({ theme }) => theme.radius.md};
    background: ${({ theme }) => theme.color.bgSurface};
    padding: ${({ theme }) => theme.spacing.md};
  `,
  CalendarHeader: styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: ${({ theme }) => theme.spacing.sm};
  `,
  CalendarNavButton: styled.button`
    ${clickableReset}
    ${focusRing}
    color: ${({ theme }) => theme.color.textSecondary};
    border: 1px solid ${({ theme }) => theme.color.borderSubtle};
    border-radius: ${({ theme }) => theme.radius.sm};
    width: 34px;
    height: 34px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
  `,
  CalendarGrid: styled.div`
    display: grid;
    grid-template-columns: repeat(7, minmax(0, 1fr));
    gap: ${({ theme }) => theme.spacing.xs};
  `,
  CalendarWeekday: styled.div`
    color: ${({ theme }) => theme.color.textTertiary};
    font-size: ${({ theme }) => theme.typography.metaSize};
    font-family: ${({ theme }) => theme.typography.monoFamily};
    text-align: center;
    text-transform: uppercase;
    padding-bottom: ${({ theme }) => theme.spacing.xs};
  `,
  CalendarDay: styled.button<{
    $selected: boolean;
    $currentMonth: boolean;
  }>`
    ${clickableReset}
    ${focusRing}
    ${({ theme, $selected, $currentMonth }) =>
      $selected
        ? css`
            background: ${theme.color.brand};
            color: ${theme.color.textInverse};
            border: 1px solid ${theme.color.brand};
          `
        : css`
            background: ${theme.color.bgSurface};
            color: ${$currentMonth ? theme.color.textPrimary : theme.color.textTertiary};
            border: 1px solid transparent;
          `}
    min-height: 52px;
    border-radius: ${({ theme }) => theme.radius.sm};
    padding: ${({ theme }) => theme.spacing.sm};
    display: grid;
    align-content: start;
    gap: ${({ theme }) => theme.spacing.xs};
    cursor: pointer;
    transition:
      border-color 160ms ${enterCurve},
      background-color 160ms ${enterCurve},
      color 160ms ${enterCurve};

    @media (hover: hover) and (pointer: fine) {
      &:hover {
        border-color: ${({ theme, $selected }) => ($selected ? theme.color.brand : theme.color.borderStrong)};
        background: ${({ theme, $selected }) => ($selected ? theme.color.brand : theme.color.bgSurfaceAlt)};
      }
    }
  `,
  FloatingPanel: styled(motion.div)<{
    $width: number;
    $maxHeight: number;
    $placement: FloatingPlacement;
    $top: number;
    $left: number;
  }>`
    position: fixed;
    top: ${({ $top }) => `${$top}px`};
    left: ${({ $left }) => `${$left}px`};
    width: ${({ $width }) => `${$width}px`};
    max-height: ${({ $maxHeight }) => `${$maxHeight}px`};
    background: ${({ theme }) => theme.color.bgSurface};
    border: 1px solid ${({ theme }) => theme.color.borderStrong};
    border-radius: ${({ theme }) => theme.radius.md};
    box-shadow: ${({ theme }) => theme.shadow.floating};
    z-index: ${({ theme }) => theme.zIndex.dropdown};
    overflow: hidden;
    transform: ${({ $placement }) => ($placement === "top" ? "translateY(-100%)" : "none")};
  `,
  DropdownTriggerButton: styled(motion.button)<{ $placeholder: boolean }>`
    ${clickableReset}
    ${focusRing}
    width: 100%;
    align-items: center;
    color: ${({ theme, $placeholder }) => ($placeholder ? theme.color.textTertiary : theme.color.textPrimary)};
    display: inline-flex;
    gap: ${({ theme }) => theme.spacing.sm};
    justify-content: space-between;
    min-height: 0;
    padding: 0;
    text-transform: none;
    letter-spacing: 0;
  `,
  DropdownTriggerValue: styled.span`
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  `,
  DropdownList: styled.div`
    max-height: inherit;
    overflow-y: auto;
    padding: ${({ theme }) => theme.spacing.xs};
  `,
  DropdownSearchWrap: styled.div`
    padding: ${({ theme }) => theme.spacing.md} ${({ theme }) => theme.spacing.md} 0;
  `,
  DropdownSearch: styled.input`
    width: 100%;
    border: 1px solid ${({ theme }) => theme.color.borderSubtle};
    border-radius: ${({ theme }) => theme.radius.sm};
    background: ${({ theme }) => theme.color.bgSurfaceAlt};
    padding: 10px 12px;
    font: inherit;
    color: ${({ theme }) => theme.color.textPrimary};
    outline: none;
    transition:
      border-color 160ms ${enterCurve},
      box-shadow 160ms ${enterCurve};

    &:focus {
      border-color: ${({ theme }) => theme.color.borderFocus};
      box-shadow: 0 0 0 3px ${({ theme }) => theme.color.brandSoft};
    }
  `,
  DropdownEmptyState: styled.div`
    padding: 10px 12px;
    color: ${({ theme }) => theme.color.textSecondary};
    font-size: ${({ theme }) => theme.typography.captionSize};
    line-height: ${({ theme }) => theme.typography.lineHeightBody};
  `,
  DropdownOptionButton: styled.button<{ $selected: boolean }>`
    ${clickableReset}
    ${focusRing}
    width: 100%;
    text-align: left;
    display: grid;
    gap: 2px;
    padding: 10px 12px;
    border-radius: ${({ theme }) => theme.radius.sm};
    cursor: pointer;
    background: ${({ theme, $selected }) => ($selected ? theme.color.bgSurfaceAlt : "transparent")};
    color: ${({ theme }) => theme.color.textPrimary};
    transition: background-color 160ms ${enterCurve}, color 160ms ${enterCurve};

    @media (hover: hover) and (pointer: fine) {
      &:hover {
        background: ${({ theme }) => theme.color.bgSurfaceAlt};
      }
    }

    &:disabled {
      cursor: not-allowed;
      color: ${({ theme }) => theme.color.textTertiary};
    }
  `,
  DropdownOptionRow: styled.div`
    display: flex;
    justify-content: space-between;
    gap: ${({ theme }) => theme.spacing.md};
  `,
  TooltipAnchor: styled.span`
    display: inline-flex;
  `,
  TooltipPanel: styled(motion.div)<{
    $maxWidth: string;
    $top: number;
    $left: number;
    $placement: TooltipPlacement;
  }>`
    position: fixed;
    top: ${({ $top }) => `${$top}px`};
    left: ${({ $left }) => `${$left}px`};
    max-width: ${({ $maxWidth }) => $maxWidth};
    background: ${({ theme }) => theme.color.textPrimary};
    color: ${({ theme }) => theme.color.textInverse};
    border-radius: ${({ theme }) => theme.radius.sm};
    padding: 8px 10px;
    box-shadow: ${({ theme }) => theme.shadow.medium};
    z-index: ${({ theme }) => theme.zIndex.tooltip};
    font-size: ${({ theme }) => theme.typography.captionSize};
    line-height: ${({ theme }) => theme.typography.lineHeightBody};
    pointer-events: none;
    transform: ${({ $placement }) =>
      $placement === "top" ? "translate(-50%, -100%)" : "translate(-50%, 0)"};
  `,
  ModalBackdrop: styled(motion.div)`
    position: fixed;
    inset: 0;
    background: ${({ theme }) => theme.color.bgOverlay};
    backdrop-filter: blur(4px);
    z-index: ${({ theme }) => theme.zIndex.overlay};
  `,
  ModalShell: styled(motion.section)<{ $mobileSheet: boolean }>`
    position: fixed;
    inset: 50% auto auto 50%;
    transform: translate(-50%, -50%);
    width: min(92vw, var(--minimal-modal-max-width, 520px));
    max-height: var(--minimal-modal-max-height, calc(100dvh - 48px));
    background: ${({ theme }) => theme.color.bgSurface};
    border: 1px solid ${({ theme }) => theme.color.borderStrong};
    border-radius: ${({ theme }) => theme.radius.md};
    box-shadow: ${({ theme }) => theme.shadow.floating};
    padding: ${({ theme }) => theme.spacing.lg};
    z-index: ${({ theme }) => theme.zIndex.modal};
    display: grid;
    gap: ${({ theme }) => theme.spacing.md};
    overflow: hidden;

    @media (max-width: 720px) {
      inset: ${({ $mobileSheet }) => ($mobileSheet ? "auto 0 0 0" : "50% auto auto 50%")};
      transform: ${({ $mobileSheet }) => ($mobileSheet ? "none" : "translate(-50%, -50%)")};
      width: ${({ $mobileSheet }) => ($mobileSheet ? "100%" : "min(92vw, var(--minimal-modal-max-width, 520px))")};
      max-height: min(90dvh, var(--minimal-modal-max-height, 90dvh));
      border-radius: ${({ theme, $mobileSheet }) =>
        $mobileSheet ? `${theme.radius.lg} ${theme.radius.lg} 0 0` : theme.radius.md};
      padding: ${({ theme }) => `${theme.spacing.lg} ${theme.spacing.md}`};

      ${({ $mobileSheet }) =>
        $mobileSheet
          ? css`
              border-right: 0;
              border-bottom: 0;
              border-left: 0;
            `
          : null}
    }
  `,
  ModalHeader: styled.div`
    display: grid;
    gap: ${({ theme }) => theme.spacing.sm};
  `,
  ModalTitle: styled.h2`
    margin: 0;
    color: ${({ theme }) => theme.color.textPrimary};
    font-family: ${({ theme }) => theme.typography.displayFamily};
    font-size: ${({ theme }) => theme.typography.h1Size};
    line-height: ${({ theme }) => theme.typography.lineHeightTight};
    letter-spacing: 0;
  `,
  ModalActions: styled.div`
    display: flex;
    justify-content: flex-end;
    gap: ${({ theme }) => theme.spacing.sm};
    flex-wrap: wrap;
  `,
  ModalBody: styled.div<{ $scrollable: boolean }>`
    min-height: 0;
    overflow-y: ${({ $scrollable }) => ($scrollable ? "auto" : "visible")};
    padding-right: ${({ $scrollable }) => ($scrollable ? "4px" : "0")};
  `,
  DisplaySection: styled(motion.section)<{
    $anchor: LandingAnchor;
    $visualMode: LandingVisualMode;
    $intensity: LandingIntensity;
    $minHeight: string;
    $backgroundImage?: string;
    $overlay?: string;
  }>`
    position: relative;
    isolation: isolate;
    display: grid;
    align-items: ${({ $anchor }) => ($anchor === "bottom-left" || $anchor === "bottom-right" ? "end" : "center")};
    min-height: ${({ $minHeight }) => $minHeight};
    overflow: hidden;
    border-radius: ${({ theme }) => theme.radius.md};
    border: 1px solid ${({ theme }) => theme.color.borderSubtle};
    background:
      ${({ $overlay }) => $overlay ?? "linear-gradient(180deg, rgba(255, 255, 255, 0.72), rgba(255, 255, 255, 0.92))"},
      ${({ $backgroundImage }) => ($backgroundImage ? `url(${$backgroundImage}) center / cover` : "transparent")};
    box-shadow: ${({ theme, $intensity }) => ($intensity === "statement" ? theme.shadow.medium : theme.shadow.subtle)};
    padding: ${({ theme, $intensity }) =>
      $intensity === "statement" ? theme.spacing.xl : $intensity === "calm" ? theme.spacing.lg : theme.spacing.xl};

    ${({ theme, $visualMode, $anchor }) =>
      $visualMode === "background" || $visualMode === "canvas"
        ? css`
            color: ${theme.color.textPrimary};
          `
        : css`
            grid-template-columns: ${$anchor === "right-visual" || $anchor === "left-visual"
              ? $anchor === "right-visual"
                ? "minmax(0, 0.9fr) minmax(320px, 1.1fr)"
                : "minmax(320px, 1.1fr) minmax(0, 0.9fr)"
              : "minmax(0, 1fr)"};
            gap: ${theme.spacing.xl};
            background-color: ${theme.color.bgSurface};
          `}

    @media (max-width: 860px) {
      grid-template-columns: 1fr;
      min-height: min(760px, max(520px, 76dvh));
      padding: ${({ theme }) => theme.spacing.lg};
    }
  `,
  DisplayCopy: styled.div<{ $anchor: LandingAnchor; $intensity: LandingIntensity }>`
    position: relative;
    z-index: 1;
    display: grid;
    gap: ${({ theme }) => theme.spacing.md};
    max-width: ${({ $intensity }) => ($intensity === "statement" ? "760px" : "620px")};
    justify-self: ${({ $anchor }) =>
      $anchor === "center" || $anchor === "stacked"
        ? "center"
        : $anchor === "bottom-right"
          ? "end"
          : "start"};
    align-self: ${({ $anchor }) => ($anchor === "top-left" ? "start" : $anchor.includes("bottom") ? "end" : "center")};
    text-align: ${({ $anchor }) => ($anchor === "center" || $anchor === "stacked" ? "center" : "left")};
  `,
  DisplayTitle: styled.h1<{ $intensity: LandingIntensity }>`
    margin: 0;
    color: ${({ theme }) => theme.color.textPrimary};
    font-family: ${({ theme }) => theme.typography.displayFamily};
    font-size: ${({ theme, $intensity }) =>
      $intensity === "statement" ? theme.typography.displaySize : theme.typography.h1Size};
    line-height: ${({ theme }) => theme.typography.lineHeightTight};
    letter-spacing: 0;
  `,
  DisplayVisual: styled.div<{ $anchor: LandingAnchor; $aspect: string }>`
    position: relative;
    z-index: 1;
    min-width: 0;
    aspect-ratio: ${({ $aspect }) => $aspect};
    width: 100%;
    align-self: stretch;
    justify-self: stretch;
    order: ${({ $anchor }) => ($anchor === "left-visual" ? -1 : 0)};
    overflow: hidden;
    border-radius: ${({ theme }) => theme.radius.md};

    > * {
      width: 100%;
      height: 100%;
    }

    @media (max-width: 860px) {
      order: 0;
      max-height: 360px;
    }
  `,
  LandingSection: styled.section<{ $anchor: LandingAnchor; $intensity: LandingIntensity }>`
    display: grid;
    gap: ${({ theme, $intensity }) => ($intensity === "calm" ? theme.spacing.md : theme.spacing.lg)};
    padding: ${({ theme, $intensity }) =>
      $intensity === "statement" ? `${theme.spacing["2xl"]} 0` : `${theme.spacing.xl} 0`};
    align-items: center;
    grid-template-columns: ${({ $anchor }) =>
      $anchor === "left-visual"
        ? "minmax(320px, 1fr) minmax(0, 0.9fr)"
        : $anchor === "right-visual"
          ? "minmax(0, 0.9fr) minmax(320px, 1fr)"
          : "minmax(0, 1fr)"};

    @media (max-width: 860px) {
      grid-template-columns: 1fr;
      padding: ${({ theme }) => `${theme.spacing.xl} 0`};
    }
  `,
  LandingCopy: styled.div<{ $anchor: LandingAnchor }>`
    display: grid;
    gap: ${({ theme }) => theme.spacing.sm};
    max-width: 680px;
    justify-self: ${({ $anchor }) => ($anchor === "center" || $anchor === "stacked" ? "center" : "start")};
    text-align: ${({ $anchor }) => ($anchor === "center" || $anchor === "stacked" ? "center" : "left")};
  `,
  LandingMedia: styled.div<{ $anchor: LandingAnchor; $aspect: string }>`
    min-width: 0;
    width: 100%;
    aspect-ratio: ${({ $aspect }) => $aspect};
    order: ${({ $anchor }) => ($anchor === "left-visual" ? -1 : 0)};
    overflow: hidden;
    border-radius: ${({ theme }) => theme.radius.md};
    border: 1px solid ${({ theme }) => theme.color.borderSubtle};
    background: ${({ theme }) => theme.color.bgSurfaceAlt};

    > * {
      width: 100%;
      height: 100%;
    }
  `,
  InfoPanel: styled.article<{ $tone: MinimalTone; $layout: InfoLayout }>`
    ${({ theme, $tone }) => {
      const accent = toneAccent(theme, $tone);
      return css`
        --minimal-info-accent: ${accent.color};
        --minimal-info-bg: ${accent.soft};
      `;
    }}
    display: grid;
    grid-template-columns: ${({ $layout }) =>
      $layout === "row" ? "auto minmax(0, 1fr) auto" : $layout === "split" ? "minmax(0, 1fr) auto" : "1fr"};
    gap: ${({ theme }) => theme.spacing.md};
    align-items: start;
    min-width: 0;
    padding: ${({ theme }) => theme.spacing.md};
    border: 1px solid ${({ theme }) => theme.color.borderSubtle};
    border-left: 2px solid var(--minimal-info-accent);
    border-radius: ${({ theme }) => theme.radius.sm};
    background: ${({ theme }) => theme.color.bgSurface};

    @media (max-width: 640px) {
      grid-template-columns: 1fr;
    }
  `,
  InfoIcon: styled.div`
    width: 36px;
    height: 36px;
    border-radius: ${({ theme }) => theme.radius.sm};
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: var(--minimal-info-bg);
    color: var(--minimal-info-accent);
  `,
  InfoCopy: styled.div`
    display: grid;
    gap: ${({ theme }) => theme.spacing.xs};
    min-width: 0;
  `,
  InfoTitle: styled.h3`
    margin: 0;
    color: ${({ theme }) => theme.color.textPrimary};
    font-size: ${({ theme }) => theme.typography.h2Size};
    line-height: ${({ theme }) => theme.typography.lineHeightTight};
  `,
  Skeleton: styled.span<{
    $width?: string;
    $height?: string;
    $inline: boolean;
    $radius?: string;
  }>`
    display: ${({ $inline }) => ($inline ? "inline-flex" : "block")};
    width: ${({ $width }) => $width ?? "100%"};
    height: ${({ $height }) => $height ?? "1rem"};
    border-radius: ${({ theme, $radius }) => $radius ?? theme.radius.md};
    background: linear-gradient(
      90deg,
      ${({ theme }) => theme.color.bgSurfaceAlt} 0%,
      ${({ theme }) => theme.color.bgSurface} 50%,
      ${({ theme }) => theme.color.bgSurfaceAlt} 100%
    );
    background-size: 200% 100%;
    animation: ${skeletonSweep} 1.2s linear infinite;

    @media (prefers-reduced-motion: reduce) {
      animation: none;
    }
  `,
} as const;

const {
  HeaderShell,
  HeaderTop,
  HeaderCopy,
  HeaderKicker,
  HeaderTitle,
  HeaderSubtitle,
  HeaderMeta,
  ButtonShell,
  Spinner,
  CardShell,
  CardSlot,
  FieldShell,
  FieldLabel,
  FieldDescription,
  InputFrame,
  InputAdornment,
  InputField,
  FieldMessage,
  BadgeShell,
  AlertShell,
  AlertIcon,
  AlertBody,
  AlertTitle,
  EmptyStateShell,
  EmptyIcon,
  EmptyStateTitle,
  FilterBarShell,
  FilterChip,
  SegmentedShell,
  SegmentedIndicator,
  SegmentedButton,
  ExplainerShell,
  ExplainerToggle,
  ExplainerCopy,
  ExplainerText,
  ExplainerActions,
  ExplainerPanel,
  ExplainerPanelBody,
  StatShell,
  StatMeta,
  StatIcon,
  StatLabel,
  StatValue,
  StatHint,
  StatTitle,
  FormSectionShell,
  FormSectionHeader,
  FormSectionCopy,
  FormSectionTitle,
  FieldGridShell,
  ActionRowShell,
  TableShell,
  StyledTable,
  TableHeaderCell,
  TableCell,
  CalendarShell,
  CalendarHeader,
  CalendarNavButton,
  CalendarGrid,
  CalendarWeekday,
  CalendarDay,
  FloatingPanel,
  DropdownTriggerButton,
  DropdownTriggerValue,
  DropdownList,
  DropdownSearchWrap,
  DropdownSearch,
  DropdownEmptyState,
  DropdownOptionButton,
  DropdownOptionRow,
  TooltipAnchor,
  TooltipPanel,
  ModalBackdrop,
  ModalShell,
  ModalHeader,
  ModalTitle,
  ModalActions,
  ModalBody,
  DisplaySection,
  DisplayCopy,
  DisplayTitle,
  DisplayVisual,
  LandingSection,
  LandingCopy,
  LandingMedia,
  InfoPanel,
  InfoIcon,
  InfoCopy,
  InfoTitle,
  Skeleton,
} = Style;

const Chevron = ({ open }: { open: boolean }) => (
  <motion.svg
    width="14"
    height="14"
    viewBox="0 0 20 20"
    fill="none"
    animate={{ rotate: open ? 180 : 0 }}
    transition={{ duration: 0.18 }}
  >
    <path
      d="M5 7.5 10 12.5 15 7.5"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  </motion.svg>
);

const isBrowser = () => typeof window !== "undefined" && typeof document !== "undefined";

const formatSearchableText = <T extends string>(option: MinimalOption<T>) =>
  [option.searchableText, option.label, option.description]
    .filter(Boolean)
    .map((value) => String(value).toLowerCase())
    .join(" ");

const normalizeDateValue = (value?: Date | string | null) => {
  if (!value) {
    return null;
  }
  const date = value instanceof Date ? new Date(value) : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return null;
  }
  date.setHours(0, 0, 0, 0);
  return date;
};

const sameDay = (left: Date | null, right: Date | null) =>
  Boolean(left && right && left.getTime() === right.getTime());

const addMonths = (date: Date, delta: number) => new Date(date.getFullYear(), date.getMonth() + delta, 1);

const startOfWeek = (date: Date, weekStartsOn: 0 | 1) => {
  const clone = new Date(date);
  const offset = (clone.getDay() - weekStartsOn + 7) % 7;
  clone.setDate(clone.getDate() - offset);
  clone.setHours(0, 0, 0, 0);
  return clone;
};

const buildMonthGrid = (month: Date, weekStartsOn: 0 | 1) => {
  const monthStart = new Date(month.getFullYear(), month.getMonth(), 1);
  const gridStart = startOfWeek(monthStart, weekStartsOn);
  return Array.from({ length: 42 }, (_, index) => {
    const day = new Date(gridStart);
    day.setDate(gridStart.getDate() + index);
    return day;
  });
};

const useDismissLayer = (refs: ReadonlyArray<{ current: HTMLElement | null }>, enabled: boolean, onDismiss: () => void) => {
  useEffect(() => {
    if (!enabled || !isBrowser()) {
      return;
    }

    const onPointer = (event: MouseEvent | TouchEvent) => {
      const target = event.target as Node | null;
      if (!target) {
        return;
      }
      const inside = refs.some((ref) => ref.current?.contains(target));
      if (!inside) {
        onDismiss();
      }
    };

    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onDismiss();
      }
    };

    document.addEventListener("mousedown", onPointer);
    document.addEventListener("touchstart", onPointer);
    document.addEventListener("keydown", onKey);

    return () => {
      document.removeEventListener("mousedown", onPointer);
      document.removeEventListener("touchstart", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [enabled, onDismiss, refs]);
};

const useFloatingPosition = (
  anchorRef: { current: HTMLElement | null },
  open: boolean,
  panelHeight = 320,
  minWidth = 0,
  matchTriggerWidth = true,
) => {
  const [position, setPosition] = useState<FloatingPosition | null>(null);

  useEffect(() => {
    if (!open || !isBrowser()) {
      return;
    }

    const update = () => {
      const anchor = anchorRef.current;
      if (!anchor) {
        return;
      }
      const rect = anchor.getBoundingClientRect();
      const viewportPadding = 12;
      const spaceBelow = window.innerHeight - rect.bottom;
      const placement: FloatingPlacement =
        spaceBelow < panelHeight && rect.top > spaceBelow ? "top" : "bottom";
      const availableHeight =
        placement === "bottom"
          ? Math.max(120, window.innerHeight - rect.bottom - floatingYOffset - viewportPadding)
          : Math.max(120, rect.top - floatingYOffset - viewportPadding);
      const requestedWidth = matchTriggerWidth ? rect.width : Math.max(rect.width, minWidth);
      const width = Math.min(requestedWidth, window.innerWidth - viewportPadding * 2);
      const left = Math.min(
        Math.max(viewportPadding, rect.left),
        Math.max(viewportPadding, window.innerWidth - width - viewportPadding),
      );

      setPosition({
        top: placement === "bottom" ? rect.bottom + floatingYOffset : rect.top - floatingYOffset,
        left,
        width,
        maxHeight: Math.min(panelHeight, availableHeight),
        placement,
      });
    };

    update();
    window.addEventListener("resize", update);
    window.addEventListener("scroll", update, true);
    return () => {
      window.removeEventListener("resize", update);
      window.removeEventListener("scroll", update, true);
    };
  }, [anchorRef, matchTriggerWidth, minWidth, open, panelHeight]);

  return position;
};

const forwardInputRef = (ref: ForwardedRef<HTMLInputElement>, element: HTMLInputElement | null) => {
  if (typeof ref === "function") {
    ref(element);
    return;
  }
  if (ref) {
    ref.current = element;
  }
};

export const scrollMinimalMainToTop = (
  behavior: MinimalScrollBehavior = "auto",
  root?: ParentNode,
) => {
  if (!isBrowser()) {
    return;
  }
  const target = (root ?? document).querySelector<HTMLElement>(`[${minimalMainScrollAttribute}="true"]`);
  if (!target) {
    return;
  }
  try {
    target.scrollTo({ top: 0, left: 0, behavior });
  } catch {
    target.scrollTop = 0;
    target.scrollLeft = 0;
  }
};

export const useMinimalScrollFeedback = (
  containerRef: RefObject<HTMLElement | null>,
  options: MinimalScrollFeedbackOptions = {},
): MinimalScrollFeedback => {
  const { reducedMotion } = useMinimalMotion();
  const { enabled = true, maxSkew = 2, minScale = 0.998 } = options;
  const { scrollY } = useScroll({ container: containerRef });
  const scrollVelocity = useVelocity(scrollY);
  const skew = useTransform(scrollVelocity, [-2000, 2000], [-maxSkew, maxSkew]);
  const scale = useTransform(scrollVelocity, [-3000, 0, 3000], [minScale, 1, minScale]);
  const smoothSkew = useSpring(skew, { stiffness: 400, damping: 60, mass: 0.5 });
  const smoothScale = useSpring(scale, { stiffness: 400, damping: 60, mass: 0.5 });

  if (reducedMotion || !enabled) {
    return { skewY: 0, scale: 1 };
  }

  return { skewY: smoothSkew, scale: smoothScale };
};

const ShellStyle = {
  App: styled.div`
    min-height: 100dvh;
    display: flex;
    flex-direction: row;
    isolation: isolate;
    background: ${({ theme }) => theme.color.bgApp};
    color: ${({ theme }) => theme.color.textPrimary};
  `,
  SkipLink: styled.a`
    position: absolute;
    top: -44px;
    left: ${({ theme }) => theme.spacing.md};
    z-index: ${({ theme }) => theme.zIndex.tooltip + 1};
    padding: ${({ theme }) => `${theme.spacing.sm} ${theme.spacing.md}`};
    border-radius: ${({ theme }) => theme.radius.md};
    background: ${({ theme }) => theme.color.brand};
    color: ${({ theme }) => theme.color.textInverse};
    font-size: ${({ theme }) => theme.typography.metaSize};
    font-weight: ${({ theme }) => theme.typography.weightSemibold};
    text-decoration: none;
    transition: top 160ms ${enterCurve};

    &:focus {
      top: ${({ theme }) => theme.spacing.sm};
      outline: 2px solid ${({ theme }) => theme.color.borderFocus};
      outline-offset: 2px;
    }
  `,
  Sidebar: styled.aside<{ $width: string; $bannerOffset: string }>`
    position: fixed;
    inset: 0 auto 0 0;
    width: ${({ $width }) => $width};
    display: flex;
    flex-direction: column;
    gap: ${({ theme }) => theme.spacing.md};
    padding: ${({ theme, $bannerOffset }) => `calc(${$bannerOffset} + ${theme.spacing.xl}) ${theme.spacing.md} ${theme.spacing.lg}`};
    overflow-y: auto;
    overscroll-behavior: contain;
    background: linear-gradient(180deg, ${({ theme }) => theme.color.bgSurface} 0%, ${({ theme }) => theme.color.bgSurfaceAlt} 100%);
    border-right: 1px solid ${({ theme }) => theme.color.borderSubtle};
    box-shadow: ${({ theme }) => theme.shadow.subtle};
    z-index: ${({ theme }) => theme.zIndex.sticky};
    transform: translateZ(0);
  `,
  Main: styled(motion.main)<{ $sidebarWidth: string; $bannerOffset: string; $mobile: boolean; $compact: boolean }>`
    flex: 1;
    min-height: 100dvh;
    height: 100dvh;
    width: 100%;
    max-width: ${({ $mobile, $sidebarWidth }) => ($mobile ? "100%" : `calc(100% - ${$sidebarWidth})`)};
    margin-left: ${({ $mobile, $sidebarWidth }) => ($mobile ? "0" : $sidebarWidth)};
    padding-top: ${({ theme, $mobile, $bannerOffset }) =>
      $mobile ? `calc(${$bannerOffset} + env(safe-area-inset-top, 0px))` : `calc(${$bannerOffset} + ${theme.spacing.xl})`};
    padding-right: ${({ theme, $mobile, $compact }) => ($mobile && $compact ? theme.spacing.sm : theme.spacing.lg)};
    padding-bottom: ${({ theme, $mobile, $compact }) =>
      $mobile ? `calc(${$compact ? "64px" : "72px"} + env(safe-area-inset-bottom, 0px))` : theme.spacing.xl};
    padding-left: ${({ theme, $mobile, $compact }) => ($mobile && $compact ? theme.spacing.sm : theme.spacing.lg)};
    overflow-y: auto;
    overscroll-behavior: contain;
    -webkit-overflow-scrolling: touch;
    background: ${({ theme }) => theme.color.bgApp};
    transition:
      padding 240ms ${moveCurve},
      margin-left 240ms ${moveCurve},
      background-color 240ms ${enterCurve};
  `,
  FeedbackSurface: styled(motion.div)`
    min-width: 0;
    transform-origin: center top;
    padding-top: 0;
  `,
};

export const MinimalSkipLink = ({
  href = "#main-content",
  children = "Skip to main content",
  ...props
}: MinimalSkipLinkProps) => (
  <ShellStyle.SkipLink href={href} {...props}>
    {children}
  </ShellStyle.SkipLink>
);

export const MinimalAppShell = ({
  children,
  sidebar,
  mobileNavigation,
  systemLayer,
  sidebarWidth = "252px",
  bannerOffset = "0px",
  mobile = false,
  ...props
}: MinimalAppShellProps) => (
  <ShellStyle.App data-minimal="AppShell" {...props}>
    <MinimalSkipLink />
    {!mobile && sidebar ? (
      <ShellStyle.Sidebar aria-label="Main navigation" $width={sidebarWidth} $bannerOffset={bannerOffset}>
        {sidebar}
      </ShellStyle.Sidebar>
    ) : null}
    {children}
    {mobile ? mobileNavigation : null}
    {systemLayer}
  </ShellStyle.App>
);

export const MinimalSidebar = ({
  children,
  mainRef,
  width = "252px",
  bannerOffset = "0px",
  onWheel,
  ...props
}: MinimalSidebarProps) => {
  const handleWheel = useCallback(
    (event: React.WheelEvent<HTMLElement>) => {
      onWheel?.(event);
      if (!event.defaultPrevented && mainRef?.current) {
        mainRef.current.scrollTop += event.deltaY;
      }
    },
    [mainRef, onWheel],
  );

  return (
    <ShellStyle.Sidebar $width={width} $bannerOffset={bannerOffset} onWheel={handleWheel} {...props}>
      {children}
    </ShellStyle.Sidebar>
  );
};

export const MinimalScrollMain = forwardRef<HTMLElement, MinimalScrollMainProps>(function MinimalScrollMain(
  {
    children,
    id = "main-content",
    sidebarWidth = "252px",
    bannerOffset = "0px",
    mobile = false,
    compact = false,
    scrollAttribute = minimalMainScrollAttribute,
    ...props
  },
  ref,
) {
  return (
    <ShellStyle.Main
      id={id}
      ref={ref}
      tabIndex={-1}
      $sidebarWidth={sidebarWidth}
      $bannerOffset={bannerOffset}
      $mobile={mobile}
      $compact={compact}
      {...{ [scrollAttribute]: "true" }}
      {...props}
    >
      {children}
    </ShellStyle.Main>
  );
});

export const MinimalScrollFeedbackSurface = ({
  children,
  feedback,
  style,
  ...props
}: MinimalScrollFeedbackSurfaceProps) => {
  const feedbackStyle = feedback
    ? ({ skewY: feedback.skewY, scale: feedback.scale } as CSSProperties)
    : undefined;

  return (
    <ShellStyle.FeedbackSurface style={{ ...feedbackStyle, ...style }} {...props}>
      {children}
    </ShellStyle.FeedbackSurface>
  );
};

export const MinimalHeader = ({
  kicker,
  title,
  subtitle,
  description,
  meta,
  actions,
  align = "start",
  titleAs = "h1",
  children,
  ...props
}: MinimalHeaderProps) => {
  const { slideUpVariants } = useMinimalMotion();

  return (
    <HeaderShell
      data-minimal="Header"
      $align={align}
      variants={slideUpVariants}
      initial="initial"
      animate="animate"
      exit="exit"
      {...props}
    >
      <HeaderTop>
        <HeaderCopy>
          {kicker ? <HeaderKicker>{kicker}</HeaderKicker> : null}
          <HeaderTitle as={titleAs}>{title}</HeaderTitle>
          {subtitle ? <HeaderSubtitle>{subtitle}</HeaderSubtitle> : null}
          {description ? <HeaderSubtitle>{description}</HeaderSubtitle> : null}
          {meta ? <HeaderMeta>{meta}</HeaderMeta> : null}
        </HeaderCopy>
        {actions ? <div>{actions}</div> : null}
      </HeaderTop>
      {children}
    </HeaderShell>
  );
};

export const MinimalDisplaySection = ({
  eyebrow,
  title,
  description,
  actions,
  visual,
  anchor = "center",
  visualMode = visual ? "inline" : "none",
  intensity = "statement",
  minHeight = "min(820px, 82dvh)",
  mediaAspectRatio = "4 / 3",
  backgroundImage,
  overlay,
  children,
  ...props
}: MinimalDisplaySectionProps) => {
  const { slideUpVariants } = useMinimalMotion();

  return (
    <DisplaySection
      data-minimal="DisplaySection"
      $anchor={anchor}
      $visualMode={visualMode}
      $intensity={intensity}
      $minHeight={minHeight}
      $backgroundImage={backgroundImage}
      $overlay={overlay}
      variants={slideUpVariants}
      initial="initial"
      animate="animate"
      exit="exit"
      {...props}
    >
      <DisplayCopy $anchor={anchor} $intensity={intensity}>
        {eyebrow ? <HeaderKicker as="span">{eyebrow}</HeaderKicker> : null}
        <DisplayTitle $intensity={intensity}>{title}</DisplayTitle>
        {description ? <HeaderSubtitle>{description}</HeaderSubtitle> : null}
        {actions ? <ActionRowShell $align={anchor === "center" || anchor === "stacked" ? "center" : "start"}>{actions}</ActionRowShell> : null}
        {children}
      </DisplayCopy>
      {visual && visualMode !== "background" && visualMode !== "canvas" ? (
        <DisplayVisual $anchor={anchor} $aspect={mediaAspectRatio}>
          {visual}
        </DisplayVisual>
      ) : null}
    </DisplaySection>
  );
};

export const MinimalLandingSection = ({
  eyebrow,
  title,
  description,
  actions,
  children,
  anchor = "stacked",
  intensity = "standard",
  media,
  mediaAspectRatio = "16 / 10",
  ...props
}: MinimalLandingSectionProps) => (
  <LandingSection data-minimal="LandingSection" $anchor={anchor} $intensity={intensity} {...props}>
    <LandingCopy $anchor={anchor}>
      {eyebrow ? <HeaderKicker as="span">{eyebrow}</HeaderKicker> : null}
      {title ? <DisplayTitle as="h2" $intensity={intensity === "statement" ? "statement" : "standard"}>{title}</DisplayTitle> : null}
      {description ? <HeaderSubtitle>{description}</HeaderSubtitle> : null}
      {actions ? <ActionRowShell $align={anchor === "center" || anchor === "stacked" ? "center" : "start"}>{actions}</ActionRowShell> : null}
    </LandingCopy>
    {media ? (
      <LandingMedia $anchor={anchor} $aspect={mediaAspectRatio}>
        {media}
      </LandingMedia>
    ) : null}
    {children}
  </LandingSection>
);

export const MinimalInfoPanel = ({
  eyebrow,
  title,
  description,
  icon,
  meta,
  action,
  tone = "neutral",
  layout = "row",
  ...props
}: MinimalInfoPanelProps) => (
  <InfoPanel data-minimal="InfoPanel" $tone={tone} $layout={layout} {...props}>
    {icon ? <InfoIcon>{icon}</InfoIcon> : null}
    <InfoCopy>
      {eyebrow ? <HeaderKicker as="span">{eyebrow}</HeaderKicker> : null}
      <InfoTitle>{title}</InfoTitle>
      {description ? <HeaderSubtitle>{description}</HeaderSubtitle> : null}
      {meta ? <FieldMessage as="div" $tone={tone}>{meta}</FieldMessage> : null}
    </InfoCopy>
    {action ? <div>{action}</div> : null}
  </InfoPanel>
);

export const MinimalButton = ({
  children,
  variant = "primary",
  tone = "brand",
  size = "md",
  fullWidth = false,
  loading = false,
  leading,
  trailing,
  disabled,
  ...props
}: MinimalButtonProps) => {
  const { micro, reducedMotion } = useMinimalMotion();
  const theme = useMinimalTheme();
  const hoverMotion = !reducedMotion && !disabled && !loading ? { y: theme.motion.hoverLift } : undefined;
  const tapMotion = !reducedMotion && !disabled && !loading ? { scale: 0.98 } : undefined;

  return (
    <ButtonShell
      data-minimal="Button"
      $variant={variant}
      $tone={tone}
      $size={size}
      $fullWidth={fullWidth}
      aria-busy={loading || undefined}
      disabled={disabled || loading}
      transition={micro}
      whileHover={hoverMotion}
      whileTap={tapMotion}
      {...props}
    >
      {loading ? <Spinner as={motion.span} animate={{ rotate: 360 }} transition={{ repeat: Infinity, duration: 1, ease: "linear" } as Transition} /> : leading}
      {children}
      {!loading ? trailing : null}
    </ButtonShell>
  );
};

export const MinimalSkeleton = ({
  width,
  height,
  inline = false,
  radius,
  ...props
}: MinimalSkeletonProps) => (
  <Skeleton
    data-minimal="Skeleton"
    aria-hidden="true"
    $width={width}
    $height={height}
    $inline={inline}
    $radius={radius}
    {...props}
  />
);

export const MinimalCard = ({
  children,
  header,
  footer,
  variant = "default",
  padding = "md",
  hoverable = false,
  ...props
}: MinimalCardProps) => {
  const { fadeVariants } = useMinimalMotion();

  return (
    <CardShell
      data-minimal="Card"
      $variant={variant}
      $padding={padding}
      $hoverable={hoverable}
      variants={fadeVariants}
      initial="initial"
      animate="animate"
      exit="exit"
      {...props}
    >
      {header ? <CardSlot>{header}</CardSlot> : null}
      <CardSlot>{children}</CardSlot>
      {footer ? <CardSlot>{footer}</CardSlot> : null}
    </CardShell>
  );
};

export const MinimalInput = forwardRef<HTMLInputElement, MinimalInputProps>(function MinimalInput(
  {
    label,
    description,
    hint,
    error,
    prefix,
    suffix,
    inputSize = "md",
    locked = false,
    containerClassName,
    disabled,
    id,
    ...props
  },
  ref
) {
  const generatedId = useId();
  const inputId = id ?? `minimal-input-${generatedId}`;
  const hintId = hint ? `${inputId}-hint` : undefined;
  const errorId = error ? `${inputId}-error` : undefined;
  const describedBy = [hintId, errorId, props["aria-describedby"]].filter(Boolean).join(" ") || undefined;
  const state: InputState = locked ? "locked" : error ? "invalid" : "default";

  return (
    <FieldShell data-minimal="Input" className={containerClassName}>
      {label ? <FieldLabel htmlFor={inputId}>{label}</FieldLabel> : null}
      {description ? <FieldDescription>{description}</FieldDescription> : null}
      <InputFrame $state={state} $size={inputSize}>
        {prefix ? <InputAdornment>{prefix}</InputAdornment> : null}
        <InputField
          {...props}
          id={inputId}
          aria-describedby={describedBy}
          aria-invalid={Boolean(error) || undefined}
          disabled={disabled || locked}
          ref={(element) => forwardInputRef(ref, element)}
        />
        {suffix ? <InputAdornment>{suffix}</InputAdornment> : null}
      </InputFrame>
      {error ? (
        <FieldMessage id={errorId} $tone="danger" role="alert">
          {error}
        </FieldMessage>
      ) : hint ? (
        <FieldMessage id={hintId} $tone="neutral">
          {hint}
        </FieldMessage>
      ) : null}
    </FieldShell>
  );
});

export const MinimalDropdown = <T extends string>({
  options,
  value,
  onChange,
  label,
  placeholder = "Select…",
  hint,
  error,
  searchable,
  searchPlaceholder = "Filter options…",
  disabled = false,
  panelMaxHeight = 320,
  panelMinWidth = 0,
  matchTriggerWidth = true,
  renderValue,
  ...props
}: MinimalDropdownProps<T>) => {
  const generatedId = useId();
  const triggerId = `minimal-dropdown-trigger-${generatedId}`;
  const listboxId = `minimal-dropdown-list-${generatedId}`;
  const showSearch = searchable ?? options.length > 7;
  const selectedOption = options.find((option) => option.value === value);
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const triggerRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const position = useFloatingPosition(triggerRef, open, panelMaxHeight, panelMinWidth, matchTriggerWidth);
  const { popVariants } = useMinimalMotion();
  const dismissRefs = useMemo(() => [triggerRef, panelRef], []);

  useDismissLayer(dismissRefs, open, () => setOpen(false));

  const filteredOptions = useMemo(() => {
    if (!showSearch || !search.trim()) {
      return options;
    }
    const query = search.trim().toLowerCase();
    return options.filter((option) => formatSearchableText(option).includes(query));
  }, [options, search, showSearch]);

  const valueContent = renderValue ? renderValue(selectedOption) : selectedOption?.label ?? placeholder;

  return (
    <FieldShell data-minimal="Dropdown" {...props}>
      {label ? <FieldLabel htmlFor={triggerId}>{label}</FieldLabel> : null}
      <InputFrame $state={error ? "invalid" : "default"} $size="md">
        <DropdownTriggerButton
          type="button"
          id={triggerId}
          ref={triggerRef}
          onClick={() => !disabled && setOpen((current) => !current)}
          disabled={disabled}
          $placeholder={!selectedOption}
        >
          <DropdownTriggerValue>{valueContent}</DropdownTriggerValue>
          <Chevron open={open} />
        </DropdownTriggerButton>
      </InputFrame>
      {error ? <FieldMessage $tone="danger">{error}</FieldMessage> : hint ? <FieldMessage $tone="neutral">{hint}</FieldMessage> : null}
      {open && position && isBrowser()
        ? createPortal(
            <FloatingPanel
              ref={panelRef}
              id={listboxId}
              role="listbox"
              aria-labelledby={label ? triggerId : undefined}
              $width={position.width}
              $maxHeight={position.maxHeight}
              $placement={position.placement}
              $top={position.top}
              $left={position.left}
              variants={popVariants}
              initial="initial"
              animate="animate"
              exit="exit"
            >
              {showSearch ? (
                <DropdownSearchWrap>
                  <DropdownSearch
                    value={search}
                    onChange={(event) => setSearch(event.target.value)}
                    placeholder={searchPlaceholder}
                    aria-label="Filter dropdown options"
                  />
                </DropdownSearchWrap>
              ) : null}
              <DropdownList>
                {filteredOptions.length === 0 ? (
                  <DropdownEmptyState>No matches found.</DropdownEmptyState>
                ) : (
                  filteredOptions.map((option) => (
                    <DropdownOptionButton
                      type="button"
                      key={option.value}
                      role="option"
                      aria-selected={option.value === value}
                      $selected={option.value === value}
                      disabled={option.disabled}
                      onClick={() => {
                        if (option.disabled) {
                          return;
                        }
                        onChange(option.value);
                        setOpen(false);
                        setSearch("");
                      }}
                    >
                      <DropdownOptionRow>
                        <span>{option.label}</span>
                        {option.meta ? <span>{option.meta}</span> : null}
                      </DropdownOptionRow>
                      {option.description ? (
                        <FieldMessage as="span" $tone="neutral">
                          {option.description}
                        </FieldMessage>
                      ) : null}
                    </DropdownOptionButton>
                  ))
                )}
              </DropdownList>
            </FloatingPanel>,
            document.body
          )
        : null}
    </FieldShell>
  );
};

export const MinimalBadge = ({
  children,
  tone = "neutral",
  emphasis = "soft",
  size = "md",
  icon,
  ...props
}: MinimalBadgeProps) => (
  <BadgeShell data-minimal="Badge" $tone={tone} $emphasis={emphasis} $size={size} {...props}>
    {icon}
    {children}
  </BadgeShell>
);

export const MinimalAlert = ({
  children,
  tone = "info",
  title,
  icon,
  action,
  ...props
}: MinimalAlertProps) => {
  const liveRole = tone === "warning" || tone === "danger" ? "alert" : "status";
  const liveMode = tone === "warning" || tone === "danger" ? "assertive" : "polite";

  return (
    <AlertShell data-minimal="Alert" $tone={tone} role={liveRole} aria-live={liveMode} {...props}>
      {icon ? <AlertIcon>{icon}</AlertIcon> : null}
      <AlertBody>
        {title ? <AlertTitle>{title}</AlertTitle> : null}
        <div>{children}</div>
        {action ? <div>{action}</div> : null}
      </AlertBody>
    </AlertShell>
  );
};

export const MinimalEmptyState = ({
  title,
  description,
  eyebrow,
  icon,
  action,
  align = "center",
  ...props
}: MinimalEmptyStateProps) => (
  <EmptyStateShell data-minimal="EmptyState" $align={align} {...props}>
    {icon ? <EmptyIcon>{icon}</EmptyIcon> : null}
    {eyebrow ? <HeaderKicker as="span">{eyebrow}</HeaderKicker> : null}
    <EmptyStateTitle>{title}</EmptyStateTitle>
    <HeaderSubtitle>{description}</HeaderSubtitle>
    {action ? <div>{action}</div> : null}
  </EmptyStateShell>
);

export const MinimalFilterBar = <T extends string>({
  value,
  options,
  onChange,
  ariaLabel,
  size = "md",
  leading,
  trailing,
  ...props
}: MinimalFilterBarProps<T>) => (
  <FilterBarShell data-minimal="FilterBar" aria-label={ariaLabel} {...props}>
    {leading}
    {options.map((option) => {
      const selected = option.value === value;
      return (
        <FilterChip
          type="button"
          key={option.value}
          $selected={selected}
          $size={size}
          aria-pressed={selected}
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </FilterChip>
      );
    })}
    {trailing}
  </FilterBarShell>
);

export const MinimalSegmentedControl = <T extends string>({
  value,
  options,
  onChange,
  ariaLabel,
  size = "md",
  disabled = false,
  ...props
}: MinimalSegmentedControlProps<T>) => {
  const { spring } = useMinimalMotion();
  const currentIndex = options.findIndex((option) => option.value === value);

  return (
    <SegmentedShell data-minimal="SegmentedControl" $size={size} role="group" aria-label={ariaLabel} {...props}>
      {currentIndex >= 0 ? (
        <SegmentedIndicator
          layout
          transition={spring}
          $index={currentIndex}
          $count={options.length}
        />
      ) : null}
      {options.map((option) => {
        const selected = option.value === value;
        return (
          <SegmentedButton
            key={option.value}
            type="button"
            $selected={selected}
            $size={size}
            $count={options.length}
            aria-pressed={selected}
            disabled={disabled || option.disabled}
            onClick={() => onChange(option.value)}
          >
            {option.label}
          </SegmentedButton>
        );
      })}
    </SegmentedShell>
  );
};

export const MinimalExplainer = ({
  title,
  description,
  children,
  icon,
  action,
  defaultOpen = false,
  open,
  onOpenChange,
  ...props
}: MinimalExplainerProps) => {
  const uncontrolled = open === undefined;
  const [localOpen, setLocalOpen] = useState(defaultOpen);
  const isOpen = uncontrolled ? localOpen : open;
  const { standard } = useMinimalMotion();

  const toggle = () => {
    const next = !isOpen;
    if (uncontrolled) {
      setLocalOpen(next);
    }
    onOpenChange?.(next);
  };

  return (
    <ExplainerShell data-minimal="Explainer" {...props}>
      <ExplainerToggle type="button" aria-expanded={isOpen} onClick={toggle}>
        <ExplainerCopy>
          {icon ? <span>{icon}</span> : null}
          <ExplainerText>
            <strong>{title}</strong>
            {description ? <FieldMessage as="span" $tone="neutral">{description}</FieldMessage> : null}
          </ExplainerText>
        </ExplainerCopy>
        <ExplainerActions>
          {action}
          <Chevron open={Boolean(isOpen)} />
        </ExplainerActions>
      </ExplainerToggle>
      <AnimatePresence initial={false}>
        {isOpen ? (
          <ExplainerPanel
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={standard}
          >
            <ExplainerPanelBody>{children}</ExplainerPanelBody>
          </ExplainerPanel>
        ) : null}
      </AnimatePresence>
    </ExplainerShell>
  );
};

export const MinimalStatCard = ({
  label,
  value,
  title,
  hint,
  trend,
  icon,
  footer,
  tone = "neutral",
  ...props
}: MinimalStatCardProps) => (
  <StatShell data-minimal="StatCard" $tone={tone} {...props}>
    <StatMeta>
      {icon ? <StatIcon>{icon}</StatIcon> : null}
      <StatLabel>{label}</StatLabel>
    </StatMeta>
    {title ? <StatTitle>{title}</StatTitle> : null}
    <StatValue>{value}</StatValue>
    {trend ? <MinimalBadge tone={tone} emphasis="soft" size="sm">{trend}</MinimalBadge> : null}
    {hint ? <StatHint>{hint}</StatHint> : null}
    {footer ? <div>{footer}</div> : null}
  </StatShell>
);

export const MinimalFormSection = ({
  title,
  description,
  action,
  children,
  ...props
}: MinimalFormSectionProps) => (
  <FormSectionShell data-minimal="FormSection" {...props}>
    <FormSectionHeader>
      <FormSectionCopy>
        <FormSectionTitle>{title}</FormSectionTitle>
        {description ? <HeaderSubtitle>{description}</HeaderSubtitle> : null}
      </FormSectionCopy>
      {action}
    </FormSectionHeader>
    {children}
  </FormSectionShell>
);

export const MinimalFieldGrid = ({
  children,
  columns = 2,
  ...props
}: MinimalFieldGridProps) => (
  <FieldGridShell data-minimal="FieldGrid" $columns={columns} {...props}>
    {children}
  </FieldGridShell>
);

export const MinimalActionRow = ({
  children,
  align = "end",
  ...props
}: MinimalActionRowProps) => (
  <ActionRowShell data-minimal="ActionRow" $align={align} {...props}>
    {children}
  </ActionRowShell>
);

export const MinimalTable = <T,>({
  rows,
  columns,
  rowKey,
  caption,
  emptyState,
  density = "comfortable",
  onRowClick,
  ...props
}: MinimalTableProps<T>) => (
  <TableShell data-minimal="Table" {...props}>
    <StyledTable $density={density}>
      {caption ? <caption>{caption}</caption> : null}
      <thead>
        <tr>
          {columns.map((column) => (
            <TableHeaderCell
              key={column.id}
              $width={column.width}
              $align={column.align ?? "left"}
            >
              <div>{column.header}</div>
              {column.headerDescription ? (
                <FieldMessage as="span" $tone="neutral">
                  {column.headerDescription}
                </FieldMessage>
              ) : null}
            </TableHeaderCell>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.length === 0 ? (
          <tr>
            <td colSpan={columns.length}>
              {emptyState ?? <MinimalEmptyState title="No rows" description="There is nothing to show yet." />}
            </td>
          </tr>
        ) : (
          rows.map((row, rowIndex) => {
            const key = rowKey ? rowKey(row, rowIndex) : rowIndex;
            const clickable = Boolean(onRowClick);
            return (
              <tr
                key={key}
                data-clickable={clickable}
                onClick={clickable ? () => onRowClick?.(row, rowIndex) : undefined}
              >
                {columns.map((column) => (
                  <TableCell
                    key={`${String(key)}:${column.id}`}
                    $align={column.align ?? "left"}
                  >
                    {column.cell(row, rowIndex)}
                  </TableCell>
                ))}
              </tr>
            );
          })
        )}
      </tbody>
    </StyledTable>
  </TableShell>
);

export const MinimalCalendar = ({
  value,
  onChange,
  month,
  onMonthChange,
  weekStartsOn = 1,
  locale = "en-US",
  renderDayContent,
  ...props
}: MinimalCalendarProps) => {
  const selectedDate = normalizeDateValue(value);
  const externalMonth = normalizeDateValue(month);
  const [internalMonth, setInternalMonth] = useState(
    externalMonth ?? selectedDate ?? new Date(new Date().getFullYear(), new Date().getMonth(), 1)
  );
  const visibleMonth = externalMonth ?? internalMonth;
  const days = useMemo(() => buildMonthGrid(visibleMonth, weekStartsOn), [visibleMonth, weekStartsOn]);
  const weekdayFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { weekday: "short" }),
    [locale]
  );
  const monthFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { month: "long", year: "numeric" }),
    [locale]
  );
  const weekDays = Array.from({ length: 7 }, (_, index) => {
    const base = new Date(2024, 0, 7 + ((index + weekStartsOn) % 7));
    return weekdayFormatter.format(base);
  });

  const updateMonth = (next: Date) => {
    if (!externalMonth) {
      setInternalMonth(next);
    }
    onMonthChange?.(next);
  };

  return (
    <CalendarShell data-minimal="Calendar" {...props}>
      <CalendarHeader>
        <CalendarNavButton type="button" onClick={() => updateMonth(addMonths(visibleMonth, -1))}>
          ‹
        </CalendarNavButton>
        <strong>{monthFormatter.format(visibleMonth)}</strong>
        <CalendarNavButton type="button" onClick={() => updateMonth(addMonths(visibleMonth, 1))}>
          ›
        </CalendarNavButton>
      </CalendarHeader>
      <CalendarGrid>
        {weekDays.map((day) => (
          <CalendarWeekday key={day}>{day}</CalendarWeekday>
        ))}
        {days.map((day) => {
          const selected = sameDay(selectedDate, day);
          const currentMonth = day.getMonth() === visibleMonth.getMonth();
          return (
            <CalendarDay
              key={day.toISOString()}
              type="button"
              $selected={selected}
              $currentMonth={currentMonth}
              onClick={() => onChange?.(day)}
            >
              <span>{day.getDate()}</span>
              {renderDayContent ? renderDayContent(day, selected, currentMonth) : null}
            </CalendarDay>
          );
        })}
      </CalendarGrid>
    </CalendarShell>
  );
};

export const MinimalTooltip = ({
  content,
  children,
  placement = "top",
  openDelay = 120,
  disabled = false,
  maxWidth = "280px",
}: MinimalTooltipProps) => {
  const [open, setOpen] = useState(false);
  const anchorRef = useRef<HTMLSpanElement>(null);
  const timeoutRef = useRef<number | null>(null);
  const { tooltipVariants } = useMinimalMotion();
  const [coords, setCoords] = useState<{ top: number; left: number } | null>(null);

  const clearTooltipTimer = () => {
    if (timeoutRef.current !== null) {
      window.clearTimeout(timeoutRef.current);
      timeoutRef.current = null;
    }
  };

  const updateCoords = () => {
    const anchor = anchorRef.current;
    if (!anchor || !isBrowser()) {
      return;
    }
    const rect = anchor.getBoundingClientRect();
    setCoords({
      left: rect.left + rect.width / 2,
      top: placement === "top" ? rect.top - 8 : rect.bottom + 8,
    });
  };

  const openTooltip = () => {
    if (disabled || !isBrowser()) {
      return;
    }
    clearTooltipTimer();
    timeoutRef.current = window.setTimeout(() => {
      updateCoords();
      setOpen(true);
    }, openDelay);
  };

  const closeTooltip = () => {
    clearTooltipTimer();
    setOpen(false);
  };

  useEffect(() => {
    if (!open || !isBrowser()) {
      return;
    }
    const handler = () => updateCoords();
    window.addEventListener("resize", handler);
    window.addEventListener("scroll", handler, true);
    return () => {
      window.removeEventListener("resize", handler);
      window.removeEventListener("scroll", handler, true);
    };
  }, [open, placement]);

  useEffect(() => () => clearTooltipTimer(), []);

  return (
    <>
      <TooltipAnchor
        data-minimal="Tooltip"
        ref={anchorRef}
        onMouseEnter={openTooltip}
        onMouseLeave={closeTooltip}
        onFocus={openTooltip}
        onBlur={closeTooltip}
      >
        {children}
      </TooltipAnchor>
      {open && coords && isBrowser()
        ? createPortal(
            <TooltipPanel
              $maxWidth={maxWidth}
              $left={coords.left}
              $top={coords.top}
              $placement={placement}
              variants={tooltipVariants}
              initial="initial"
              animate="animate"
              exit="exit"
            >
              {content}
            </TooltipPanel>,
            document.body
          )
        : null}
    </>
  );
};

export const MinimalActionModal = ({
  open,
  title,
  description,
  children,
  tone = "neutral",
  confirmLabel = "Confirm",
  cancelLabel = "Cancel",
  confirmDisabled = false,
  maxWidth = "520px",
  maxHeight = "calc(100dvh - 48px)",
  align = "start",
  bodyScrollable = true,
  mobileSheet = true,
  onClose,
  onConfirm,
}: MinimalActionModalProps) => {
  const [pending, setPending] = useState(false);
  const modalRef = useRef<HTMLElement>(null);
  const { popVariants, fadeVariants } = useMinimalMotion();
  const dismissRefs = useMemo(() => [modalRef], []);

  useDismissLayer(dismissRefs, open, onClose);

  useEffect(() => {
    if (!open || !isBrowser()) {
      return;
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  const handleConfirm = async () => {
    if (!onConfirm || pending || confirmDisabled) {
      return;
    }
    try {
      setPending(true);
      await onConfirm();
      onClose();
    } finally {
      setPending(false);
    }
  };

  if (!open || !isBrowser()) {
    return null;
  }

  return createPortal(
    <AnimatePresence>
      <ModalBackdrop
        variants={fadeVariants}
        initial="initial"
        animate="animate"
        exit="exit"
        onClick={onClose}
      />
      <ModalShell
        data-minimal="ActionModal"
        ref={modalRef}
        $mobileSheet={mobileSheet}
        role="dialog"
        aria-modal="true"
        style={{
          "--minimal-modal-max-width": maxWidth,
          "--minimal-modal-max-height": maxHeight,
        } as CSSProperties}
        variants={popVariants}
        initial="initial"
        animate="animate"
        exit="exit"
        onClick={(event) => event.stopPropagation()}
      >
        <ModalHeader style={{ textAlign: align === "center" ? "center" : "left" }}>
          <HeaderKicker as="span">{tone}</HeaderKicker>
          <ModalTitle>{title}</ModalTitle>
          {description ? <HeaderSubtitle>{description}</HeaderSubtitle> : null}
        </ModalHeader>
        {children ? <ModalBody $scrollable={bodyScrollable}>{children}</ModalBody> : null}
        <ModalActions>
          <MinimalButton variant="quiet" tone="neutral" onClick={onClose}>
            {cancelLabel}
          </MinimalButton>
          {onConfirm ? (
            <MinimalButton
              variant="primary"
              tone={tone === "neutral" ? "brand" : tone}
              loading={pending}
              disabled={confirmDisabled}
              onClick={handleConfirm}
            >
              {confirmLabel}
            </MinimalButton>
          ) : null}
        </ModalActions>
      </ModalShell>
    </AnimatePresence>,
    document.body
  );
};
