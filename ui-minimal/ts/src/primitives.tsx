import React, {
  ButtonHTMLAttributes,
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
import { css } from "@linaria/core";
import { styled } from "@linaria/react";

import { minimalEnter, minimalMotionMs } from "./motionStyles.ts";
import { useMinimalPresence } from "./presence";
import type { MinimalThemeVars } from "./tokens.ts";
import { from, minimalVars, until } from "./tokens.ts";
import { variantRules } from "./variantRules.ts";
import type {
  MinimalDensity,
  MinimalEmphasis,
  MinimalSize,
  MinimalSpaceTheme,
  MinimalTone,
} from "./types.ts";

/** A rung of the spacing scale, by name. */
export type MinimalSpaceStep = keyof MinimalSpaceTheme;

type SurfaceVariant = "default" | "muted" | "raised" | "outlined";
type HeaderAlign = "start" | "center";
type ActionAlign = "start" | "center" | "end" | "between";
type InputState = "default" | "invalid" | "locked";
type ButtonVariant = "primary" | "secondary" | "ghost" | "quiet";
type TooltipPlacement = "top" | "bottom";
type FloatingPlacement = "top" | "bottom";
type CalendarViewMode = "day" | "month" | "year";
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

export interface MinimalHeaderProps extends Omit<HTMLAttributes<HTMLElement>, "children" | "title"> {
  /** Play the mount animation. Defaults to `true`; `false` renders in place. */
  enter?: boolean;
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

export interface MinimalButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children"> {
  children?: ReactNode;
  variant?: ButtonVariant;
  tone?: MinimalTone;
  size?: MinimalSize;
  fullWidth?: boolean;
  loading?: boolean;
  leading?: ReactNode;
  trailing?: ReactNode;
}

export interface MinimalCardProps extends Omit<HTMLAttributes<HTMLElement>, "children"> {
  /** Play the mount animation. Defaults to `true`; `false` renders in place. */
  enter?: boolean;
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
  /**
   * Indicator styling. `neutral` (default) is the raised white pill; `brand`
   * fills the indicator with the theme brand colour and inverts the selected
   * label — for prominent, on-brand switches (e.g. an auth role toggle).
   */
  variant?: "neutral" | "brand";
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

/**
 * A vertical stack whose spacing an element can claim for itself.
 *
 * ## Why this is not a flex column with a `gap`
 *
 * `gap` is a property of the container; `margin` is a property of the child,
 * and that single difference is the whole reason this exists. `gap: 16px` says
 * *every child of this box is 16px from its neighbour* — a statement about a
 * set. It cannot say "more air before a section heading than after it", because
 * it does not know which child is a heading. Expressing hierarchy underneath it
 * means nesting another container for every distinct spacing, or overriding
 * with margins anyway and maintaining two spacing systems that fight.
 *
 * `margin-block-start: 40px` on a heading says *a heading claims 40px above
 * it*, and the claim travels with the element: move the heading and its rhythm
 * moves too; add another and it is already spaced. That is how typesetting has
 * worked since metal type, and it is why editorial CSS still reads better than
 * component CSS — the rhythm is a property of the content model rather than of
 * wherever the content was placed.
 *
 * ## Why flow layout, specifically
 *
 * Margins **do not collapse inside flex or grid**, and collapsing is the
 * mechanism that makes claims compose. In flow, a section claiming `xl` above
 * itself that contains a heading claiming `lg` above resolves to `xl` — not to
 * the sum. Each element states its requirement, the larger one wins, and
 * nothing needs to know about anything else. Swapping `gap` for margins while
 * keeping `flex-direction: column` gets none of that: the margins add instead
 * of resolving, a trailing margin starts pushing the container, and the result
 * is additive uniformity with more code — strictly worse than the `gap` it
 * replaced. That trap is the single most important thing to know before
 * reaching for this.
 *
 * ## What it does not replace
 *
 * Grids, wrapping rows, and genuinely uniform peer sets keep `gap`. A toolbar
 * of equal buttons *is* uniform; uniformity is the truth there and `gap` states
 * it once instead of N times. A margin on a wrapped item is applied at the wrap
 * point too, so the second row starts inset — `gap` is the only correct tool
 * for a row that reflows.
 */
export interface MinimalStackProps extends HTMLAttributes<HTMLDivElement> {
  children: ReactNode;
  /**
   * The space every child claims above itself unless it says otherwise.
   *
   * A uniform default is honest for a stack that *is* uniform; hierarchy comes
   * from children that claim more. Defaults to `sm`.
   */
  rhythm?: MinimalSpaceStep;
  /** Render as something other than a `div` — `section`, `article`, `ul`. */
  as?: "div" | "section" | "article" | "ul" | "ol" | "nav" | "aside";
}

export interface MinimalCullSectionProps extends HTMLAttributes<HTMLDivElement> {
  children: ReactNode;
  /**
   * The height this section is expected to occupy before it has ever been
   * rendered, as a CSS length. Used only until the browser has laid the section
   * out once; after that it remembers the real size (`contain-intrinsic-size:
   * auto`). An estimate close to the real height is what keeps the scrollbar
   * from jumping as sections are skipped and restored. Defaults to `600px`.
   */
  estimatedSize?: string;
  /** Render as something other than a `div` — `section`, `article`, `li`. */
  as?: "div" | "section" | "article" | "li";
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
  minDate?: Date | string | null;
  maxDate?: Date | string | null;
  isDateDisabled?: (date: Date) => boolean;
  weekStartsOn?: 0 | 1;
  locale?: string;
  /** Show the leading/trailing days required to keep a stable six-week grid. */
  showAdjacentDays?: boolean;
  /** Show a shortcut that returns the view to the current local date. */
  showTodayAction?: boolean;
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

export interface MinimalDisplaySectionProps extends Omit<HTMLAttributes<HTMLElement>, "children" | "title"> {
  /** Play the mount animation. Defaults to `true`; `false` renders in place. */
  enter?: boolean;
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

/**
 * An image always knows its box before its pixels arrive. Either the intrinsic
 * `width` and `height` (the browser derives the aspect ratio from them and
 * scales the box to the column), or an `aspectRatio` for images that fill
 * their container. There is deliberately no unsized form: an unsized image is
 * zero pixels tall until it loads and then pushes everything below it down.
 */
export type MinimalImageSize =
  | { width: number; height: number; aspectRatio?: never }
  | { aspectRatio: number | string; width?: never; height?: never };

export type MinimalImageProps = Omit<
  React.ImgHTMLAttributes<HTMLImageElement>,
  "width" | "height" | "src" | "alt"
> &
  MinimalImageSize & {
    src: string;
    /** Required; pass "" for a purely decorative image. */
    alt: string;
    /** The image is the largest thing on first screen: load eagerly at high fetch priority. */
    priority?: boolean;
    fit?: "cover" | "contain";
    /** Fade in once decoded (P6 rule 2). Client-only: never on a prerendered or LCP image. */
    reveal?: boolean;
    radius?: string;
  };

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

export interface MinimalScrollMainProps extends Omit<HTMLAttributes<HTMLElement>, "children"> {
  children: ReactNode;
  sidebarWidth?: string;
  bannerOffset?: string;
  mobile?: boolean;
  compact?: boolean;
  scrollAttribute?: string;
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

const inputPadding = {
  sm: "8px 12px",
  md: "12px 16px",
  lg: "14px 16px",
} satisfies Record<MinimalSize, string>;

const toneAccent = (theme: MinimalThemeVars, tone: MinimalTone) => {
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

const tonePresentation = (theme: MinimalThemeVars, tone: MinimalTone, emphasis: MinimalEmphasis) => {
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

const surfacePresentation = (theme: MinimalThemeVars, variant: SurfaceVariant) => {
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

/**
 * Every member of a union, as a list `variantRules` can enumerate. Fails to
 * compile when a member is missing, because a missing one would render with
 * no variant styles at all rather than with the wrong ones.
 */
const allOf =
  <U extends string>() =>
  <const T extends readonly U[]>(values: T & ([U] extends [T[number]] ? unknown : { missing: Exclude<U, T[number]> })) =>
    values;

const minimalTones = allOf<MinimalTone>()(["neutral", "brand", "info", "success", "warning", "danger"]);
const emphases = allOf<MinimalEmphasis>()(["soft", "solid", "outline"]);
const buttonVariants = allOf<ButtonVariant>()(["primary", "secondary", "ghost", "quiet"]);
const surfaceVariants = allOf<SurfaceVariant>()(["default", "muted", "raised", "outlined"]);
const inputStates = allOf<InputState>()(["default", "invalid", "locked"]);

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
/*
 * The sweep moves a highlight band with `transform`, never `background-position`.
 *
 * A `background-position` keyframe cannot run on the compositor: every skeleton
 * on screen restyles and repaints on every frame for as long as it exists. The
 * frontend lab measured it (CPU 6x, Metal GPU, 240 skeletons in a feed): the
 * shimmer was ~880 ms of style recalculation and ~1,480 ms of paint per scroll
 * pass, and the only reason `low_power` separated from `high` at all.
 */
/*
 * Placeholders sweep only while someone can see them.
 *
 * The property was not the whole cost. Interleaved in the lab (240 skeletons,
 * CPU 6x, Metal): every always-running design — transform, background-position,
 * opacity pulse — cost 0.8–2.2 s of main-thread work per scroll pass and left
 * 55–71% of frames slow, because Chromium updates every running animation each
 * frame whether it is on screen or not. Pausing the ones off screen measured
 * the same as no animation at all (p50 8.3 ms, 0% slow) and beat
 * content-visibility culling (5% slow).
 *
 * One observer for every skeleton on the page, created on first use. Absent
 * IntersectionObserver (SSR, old engines) nothing is marked and the sweep simply
 * runs, as before. The margin starts a placeholder just before it scrolls in.
 */
let skeletonVisibility: IntersectionObserver | null = null;
const observeSkeleton = (element: Element): (() => void) => {
  if (typeof IntersectionObserver === "undefined") return () => undefined;
  skeletonVisibility ??= new IntersectionObserver(
    (entries) => {
      for (const entry of entries) entry.target.toggleAttribute("data-minimal-offscreen", !entry.isIntersecting);
    },
    { rootMargin: "25% 0px" },
  );
  skeletonVisibility.observe(element);
  return () => skeletonVisibility?.unobserve(element);
};


const focusRing = `
  &:focus-visible {
    outline: 2px solid ${minimalVars.color.borderFocus};
    outline-offset: 2px;
  }
`;

const clickableReset = `
  appearance: none;
  border: 0;
  background: transparent;
  font: inherit;
`;

/* Build-time style block (Linaria evaluates it once). */
const styleBlock1 = variantRules("tone", minimalTones, (tone) => {
      const accent = toneAccent(minimalVars, tone);
      return `
        background: ${accent.color};
        color: ${minimalVars.color.textInverse};
        border: 1px solid ${accent.color};
      `;
    });
/* Build-time style block (Linaria evaluates it once). */
const styleBlock2 = variantRules("variant", buttonVariants.filter((v) => v !== "primary"), (variant) =>
      variantRules("tone", minimalTones, (tone) => {
        const accent = toneAccent(minimalVars, tone);
        if (variant === "secondary") {
          return `
            background: transparent;
            color: ${accent.color};
            border: 1px solid ${accent.color};
          `;
        }
        if (variant === "ghost") {
          return `
            background: ${accent.soft};
            color: ${accent.color};
            border: 1px solid transparent;
          `;
        }
        return `
          background: transparent;
          color: ${minimalVars.color.textSecondary};
          border: 1px solid transparent;
        `;
      })
    );
/* Build-time style block (Linaria evaluates it once). */
const styleBlock3 = variantRules("variant", ["primary"] as const, () => `
      box-shadow: ${litEdge(14)};
    `);
/* Build-time style block (Linaria evaluates it once). */
const styleBlock4 = variantRules("variant", surfaceVariants.filter((v) => v !== "default"), (variant) => {
      const surface = surfacePresentation(minimalVars, variant);
      return `
        background: ${surface.background};
        border: 1px solid ${surface.border};
        box-shadow: ${surface.shadow};
      `;
    });
/* Build-time style block (Linaria evaluates it once). */
const styleBlock5 = variantRules("hoverable", [true] as const, () => `
      @media (hover: hover) and (pointer: fine) {
        &:hover {
          border-color: ${minimalVars.color.borderStrong};
          box-shadow: ${minimalVars.shadow.floating};
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
    `);
/* Build-time style block (Linaria evaluates it once). */
const styleBlock6 = variantRules("state", inputStates, (state) => {
      const borderColor =
        state === "invalid"
          ? minimalVars.color.danger
          : state === "locked"
            ? minimalVars.color.borderStrong
            : minimalVars.color.borderSubtle;

      const background =
        state === "locked" ? minimalVars.color.bgSurfaceAlt : minimalVars.color.bgSurface;

      return `
        background: ${background};
        border: 1px solid ${borderColor};
      `;
    });
/* Build-time style block (Linaria evaluates it once). */
const styleBlock7 = variantRules("tone", minimalTones, (tone) =>
      variantRules("emphasis", emphases, (emphasis) => {
        const presentation = tonePresentation(minimalVars, tone, emphasis);
        return `
          background: ${presentation.background};
          color: ${presentation.color};
          border: 1px solid ${presentation.border};
        `;
      })
    );
/* Build-time style block (Linaria evaluates it once). */
const styleBlock8 = variantRules("tone", minimalTones, (tone) => {
      const presentation = tonePresentation(minimalVars, tone, "soft");
      return `
        background: ${presentation.background};
        color: ${presentation.color};
        border: 1px solid ${presentation.border};
        border-left-width: 2px;
      `;
    });
/* Build-time style block (Linaria evaluates it once). */
const styleBlock9 = variantRules("selected", [true, false] as const, (selected) =>
      selected
        ? `
            background: ${minimalVars.color.bgSurface};
            border: 1px solid ${minimalVars.color.borderFocus};
            color: ${minimalVars.color.textPrimary};
            box-shadow: ${minimalVars.shadow.subtle};
          `
        : `
            background: transparent;
            border: 1px solid transparent;
            color: ${minimalVars.color.textSecondary};
          `
    );
/* Build-time style block (Linaria evaluates it once). */
const styleBlock10 = variantRules("tone", minimalTones, (tone) => {
      const accent = toneAccent(minimalVars, tone);
      return `
        background: ${minimalVars.color.bgSurface};
        border: 1px solid ${minimalVars.color.borderSubtle};
        box-shadow: ${minimalVars.shadow.subtle};
        --minimal-stat-accent: ${accent.color};
        --minimal-stat-bg: ${accent.soft};
      `;
    });
/* Build-time style block (Linaria evaluates it once). */
const styleBlock11 = (Object.keys(minimalVars.space) as MinimalSpaceStep[]).map(
        (step) => `
          > [data-space="${step}"] {
            margin-block-start: ${minimalVars.space[step]};
          }
        `,
      ).join("");
/* Build-time style block (Linaria evaluates it once). */
const styleBlock12 = variantRules("selected", [true] as const, () => `
      background: transparent;
      color: ${minimalVars.color.textInverse};
      border: 1px solid transparent;
    `);
/* Build-time style block (Linaria evaluates it once). */
const styleBlock13 = variantRules("selected", [false] as const, () => `
      background: ${minimalVars.color.bgSurface};
      ${variantRules("current-month", [true, false] as const, (currentMonth) => `
        color: ${currentMonth ? minimalVars.color.textPrimary : minimalVars.color.textTertiary};
      `)}
      ${variantRules("today", [true, false] as const, (today) => `
        border: 1px solid ${today ? minimalVars.color.brand : "transparent"};
      `)}
    `);
/* Build-time style block (Linaria evaluates it once). */
const styleBlock14 = variantRules("disabled", [true] as const, () => `
      color: ${minimalVars.color.textTertiary};
      cursor: not-allowed;
      opacity: 0.46;
    `);
/* Build-time style block (Linaria evaluates it once). */
const styleBlock15 = variantRules("mobile-sheet", [true] as const, () => `
        border-right: 0;
        border-bottom: 0;
        border-left: 0;
      `);
/* Build-time style block (Linaria evaluates it once). */
const styleBlock16 = variantRules("visual-mode", ["background", "canvas"] as const, () => `
      color: ${minimalVars.color.textPrimary};
    `);
/* Build-time style block (Linaria evaluates it once). */
const styleBlock17 = variantRules("anchor", ["right-visual"] as const, () => `
        grid-template-columns: minmax(0, 0.9fr) minmax(320px, 1.1fr);
      `);
/* Build-time style block (Linaria evaluates it once). */
const styleBlock18 = variantRules("anchor", ["left-visual"] as const, () => `
        grid-template-columns: minmax(320px, 1.1fr) minmax(0, 0.9fr);
      `);
/* Build-time style block (Linaria evaluates it once). */
const styleBlock19 = variantRules("tone", minimalTones, (tone) => {
      const accent = toneAccent(minimalVars, tone);
      return `
        --minimal-info-accent: ${accent.color};
        --minimal-info-bg: ${accent.soft};
      `;
    });
const Style = {
  HeaderShell: styled.header<{ $align: HeaderAlign }>`
    display: grid;
    gap: ${minimalVars.space.xs};
    justify-items: ${({ $align }) => ($align === "center" ? "center" : "stretch")};
    text-align: ${({ $align }) => ($align === "center" ? "center" : "left")};
    ${minimalEnter.slideUp}
  `,
  HeaderTop: styled.div`
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: ${minimalVars.space.sm};
    width: 100%;
  `,
  HeaderCopy: styled.div`
    display: grid;
    gap: ${minimalVars.space["2xs"]};
    min-width: 0;
  `,
  HeaderKicker: styled.p`
    margin: 0;
    color: ${minimalVars.color.textTertiary};
    font-size: ${minimalVars.typography.metaSize};
    font-weight: ${minimalVars.typography.weightSemibold};
    letter-spacing: 0.14em;
    text-transform: uppercase;
  `,
  HeaderTitle: styled.h1`
    margin: 0;
    color: ${minimalVars.color.textPrimary};
    font-family: ${minimalVars.typography.displayFamily};
    font-size: ${minimalVars.typography.displaySize};
    line-height: ${minimalVars.typography.lineHeightTight};
    letter-spacing: 0;
  `,
  HeaderSubtitle: styled.p`
    margin: 0;
    color: ${minimalVars.color.textSecondary};
    font-size: ${minimalVars.typography.bodySize};
    line-height: ${minimalVars.typography.lineHeightBody};
  `,
  HeaderMeta: styled.div`
    color: ${minimalVars.color.textTertiary};
    font-size: ${minimalVars.typography.captionSize};
  `,
  ButtonShell: styled.button<{
    $variant: ButtonVariant;
    $tone: MinimalTone;
    $size: MinimalSize;
    $fullWidth: boolean;
  }>`
    ${clickableReset}
    ${focusRing}
    position: relative;

    &::after {
      content: "";
      position: absolute;
      top: 50%;
      left: 50%;
      transform: translate(-50%, -50%);
      min-width: 44px;
      min-height: 44px;
      width: 100%;
      height: 100%;
    }
    /* Primary is the fallthrough, as it was when this was an if-chain: every
       button gets the filled treatment for its tone, and the other variants
       override it. Same specificity, later in the sheet, so they win. */
    ${styleBlock1}
    ${styleBlock2}
    align-items: center;
    border-radius: ${minimalVars.radius.sm};
    cursor: pointer;
    display: inline-flex;
    gap: ${minimalVars.space.xs};
    justify-content: center;
    letter-spacing: 0.02em;
    line-height: 1;
    min-height: ${({ $size }) => `var(--minimal-control-height-${$size})`};
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
    font-weight: ${minimalVars.typography.weightSemibold};

    /* Filled buttons rest with a hairline lit top edge so the fill is a material,
       not a swatch. Quiet/ghost buttons stay flat until touched. */
    ${styleBlock3}

    @media (hover: hover) and (pointer: fine) {
      &:not(:disabled):hover {
        transform: translateY(-1px);
        /* The shadow is tinted to the button's own tone and cast short + soft,
           so the lift feels owned by the colour instead of a generic float. */
        box-shadow: ${({ $tone, $variant }) =>
          $variant === "quiet"
            ? "none"
            : $variant === "primary"
              ? `${litEdge(18)}, ${tonalShadow(toneAccent(minimalVars, $tone).color, 52)}`
              : tonalShadow(toneAccent(minimalVars, $tone).color, 30)};
      }
    }

    /* Active is a real press: it settles back onto the surface with a tighter,
       quicker shadow rather than fading out. */
    &:not(:disabled):active {
      transform: translateY(0);
      transition-duration: 70ms;
      box-shadow: ${({ $tone, $variant }) =>
        $variant === "quiet"
          ? "none"
          : $variant === "primary"
            ? `${litEdge(8)}, ${tonalShadow(toneAccent(minimalVars, $tone).color, 46, 3, 8, -6)}`
            : tonalShadow(toneAccent(minimalVars, $tone).color, 26, 3, 8, -6)};
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
    /* A CSS rotation, not framer-motion's: its independent rotate is written
       from JavaScript every frame (research doc section 14.2), which is the
       one animation that must keep turning while the main thread is busy. */
    @keyframes minimal-spinner-turn {
      to {
        transform: rotate(360deg);
      }
    }
    animation: minimal-spinner-turn 1s linear infinite;

    @media (prefers-reduced-motion: reduce) {
      animation-duration: 3s;
    }

    :root:where([data-ui-tier="low_power"], [data-ui-tier="reduced_motion"]) & {
      animation: none;
      border-bottom-color: currentColor;
      opacity: 0.6;
    }
  `,
  CardShell: styled.section<{ $padding: MinimalSize }>`
    /* "default" is the fallthrough, as it is in surfacePresentation: declared
       inline here, and the other variants override it from later in the sheet. */
    background: ${surfacePresentation(minimalVars, "default").background};
    border: 1px solid ${surfacePresentation(minimalVars, "default").border};
    box-shadow: ${surfacePresentation(minimalVars, "default").shadow};
    ${styleBlock4}
    border-radius: ${minimalVars.radius.md};
    display: grid;
    gap: ${minimalVars.space.xs};
    overflow: hidden;
    padding: ${({ $padding }) => cardPadding[$padding]};
    transition:
      box-shadow 220ms ${moveCurve},
      transform 220ms ${moveCurve},
      border-color 220ms ${enterCurve};
    ${minimalEnter.fade}

    ${styleBlock5}
  `,
  CardSlot: styled.div`
    min-width: 0;
  `,
  FieldShell: styled.div`
    display: grid;
    gap: ${minimalVars.space["2xs"]};
    width: 100%;
  `,
  FieldLabel: styled.label`
    color: ${minimalVars.color.textPrimary};
    font-size: ${minimalVars.typography.captionSize};
    font-weight: ${minimalVars.typography.weightSemibold};
  `,
  FieldDescription: styled.div`
    color: ${minimalVars.color.textTertiary};
    font-size: ${minimalVars.typography.captionSize};
    line-height: ${minimalVars.typography.lineHeightBody};
  `,
  InputFrame: styled.div<{ $state: InputState; $size: MinimalSize }>`
    ${styleBlock6}
    ${focusRing}
    align-items: center;
    border-radius: ${minimalVars.radius.sm};
    display: flex;
    gap: ${minimalVars.space.xs};
    min-height: ${({ $size }) => ($size === "sm" ? "36px" : $size === "lg" ? "48px" : "44px")};
    padding: ${({ $size }) => inputPadding[$size]};
    transition:
      border-color 160ms ${enterCurve},
      box-shadow 160ms ${enterCurve},
      background 160ms ${enterCurve};

    &:focus-within {
      border-color: ${({ $state }) => ($state === "invalid" ? minimalVars.color.danger : minimalVars.color.borderFocus)};
      /* Built in one interpolation rather than two. A declaration whose value
         wraps onto a following line that begins with an interpolation is
         unparseable to the CSS-in-JS language service, which then reports a
         spurious "semi-colon expected" for the whole block. */
      box-shadow: ${({ $state }) =>
        `0 0 0 ${minimalVars.focus.ringWidth} ${$state === "invalid" ? minimalVars.color.dangerSoft : minimalVars.color.brandSoft}`};
    }
  `,
  InputAdornment: styled.span`
    color: ${minimalVars.color.textSecondary};
    display: inline-flex;
    flex-shrink: 0;
    align-items: center;
  `,
  InputField: styled.input`
    background: transparent;
    border: 0;
    color: ${minimalVars.color.textPrimary};
    flex: 1;
    font: inherit;
    min-width: 0;
    outline: none;
    padding: 0;

    &:-webkit-autofill,
    &:-webkit-autofill:hover,
    &:-webkit-autofill:focus,
    &:-webkit-autofill:active {
      -webkit-text-fill-color: ${minimalVars.color.textPrimary} !important;
      caret-color: ${minimalVars.color.textPrimary};
      -webkit-box-shadow: 0 0 0 1000px ${minimalVars.color.bgSurface} inset !important;
      box-shadow: 0 0 0 1000px ${minimalVars.color.bgSurface} inset !important;
      transition: background-color 9999s ease-out 0s;
    }
  `,
  FieldMessage: styled.p<{ $tone: MinimalTone }>`
    margin: 0;
    color: ${({ $tone }) => toneAccent(minimalVars, $tone).color};
    font-size: ${minimalVars.typography.captionSize};
    line-height: ${minimalVars.typography.lineHeightBody};
  `,
  BadgeShell: styled.span<{ $size: MinimalSize }>`
    ${styleBlock7}
    align-items: center;
    border-radius: ${minimalVars.radius.sm};
    display: inline-flex;
    gap: ${minimalVars.space["2xs"]};
    justify-content: center;
    letter-spacing: 0.04em;
    line-height: 1;
    padding: ${({ $size }) => ($size === "sm" ? "3px 8px" : $size === "lg" ? "5px 10px" : "4px 9px")};
    text-transform: uppercase;
    white-space: nowrap;
    font-size: ${({ $size }) =>
      $size === "sm" ? minimalVars.typography.metaSize : minimalVars.typography.captionSize};
    font-weight: ${minimalVars.typography.weightSemibold};
  `,
  AlertShell: styled.section`
    /* The left-edge width lives inside the variant, after the border
       shorthand: the variant rule is emitted after this rule's own
       declarations, so a width declared here would be reset by it. */
    ${styleBlock8}
    border-radius: ${minimalVars.radius.sm};
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    gap: ${minimalVars.space.xs};
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
    gap: ${minimalVars.space["2xs"]};
  `,
  AlertTitle: styled.strong`
    font-size: ${minimalVars.typography.captionSize};
    text-transform: uppercase;
    letter-spacing: 0.06em;
  `,
  EmptyStateShell: styled.section<{ $align: HeaderAlign }>`
    align-items: ${({ $align }) => ($align === "center" ? "center" : "flex-start")};
    background: ${minimalVars.color.bgSurfaceAlt};
    border: 1px dashed ${minimalVars.color.borderStrong};
    border-radius: ${minimalVars.radius.md};
    display: grid;
    gap: ${minimalVars.space.xs};
    justify-items: ${({ $align }) => ($align === "center" ? "center" : "stretch")};
    padding: ${minimalVars.space.lg};
    text-align: ${({ $align }) => ($align === "center" ? "center" : "left")};
  `,
  EmptyIcon: styled.div`
    width: 42px;
    height: 42px;
    border-radius: 999px;
    border: 1px solid ${minimalVars.color.borderStrong};
    color: ${minimalVars.color.textSecondary};
    display: inline-flex;
    align-items: center;
    justify-content: center;
  `,
  EmptyStateTitle: styled.h3`
    margin: 0;
    color: ${minimalVars.color.textPrimary};
    font-family: ${minimalVars.typography.displayFamily};
    font-size: ${minimalVars.typography.h2Size};
    line-height: ${minimalVars.typography.lineHeightTight};
    letter-spacing: 0;
  `,
  FilterBarShell: styled.section`
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: ${minimalVars.space.xs};
  `,
  FilterChip: styled.button<{ $size: MinimalSize }>`
    ${clickableReset}
    ${focusRing}
    ${styleBlock9}
    border-radius: ${minimalVars.radius.sm};
    cursor: pointer;
    font-size: ${({ $size }) => sizeFont[$size]};
    font-weight: ${minimalVars.typography.weightSemibold};
    letter-spacing: 0.02em;
    line-height: 1;
    min-height: ${({ $size }) => `var(--minimal-control-height-${$size})`};
    padding: ${({ $size }) => sizePadding[$size]};
    text-transform: uppercase;
    transition:
      background-color 240ms ${moveCurve},
      border-color 240ms ${moveCurve},
      color 240ms ${moveCurve},
      box-shadow 240ms ${moveCurve};

    /* An unselected chip is quieter by colour, not by opacity. Fading text to
       84% multiplies whatever the theme's textSecondary is against whatever is
       behind it, so the resulting contrast depends on the page rather than on
       the token — and a theme that sits close to the 4.5:1 floor lands under
       it. The selected state is already distinguished by its surface, border
       and shadow, so the chip reads without the extra fade. */
    @media (hover: hover) and (pointer: fine) {
      &:hover {
        color: ${minimalVars.color.textPrimary};
      }
    }
  `,
  SegmentedShell: styled.div<{ $size: MinimalSize }>`
    position: relative;
    display: inline-flex;
    align-items: stretch;
    background: ${minimalVars.color.bgSurface};
    border: 1px solid ${minimalVars.color.borderSubtle};
    border-radius: ${minimalVars.radius.sm};
    gap: ${minimalVars.space["2xs"]};
    padding: 4px;
    min-height: ${({ $size }) => ($size === "sm" ? "34px" : $size === "lg" ? "44px" : "38px")};
  `,
  SegmentedIndicator: styled.div<{ $count: number; $index: number; $variant: "neutral" | "brand" }>`
    position: absolute;
    top: 4px;
    bottom: 4px;
    left: 4px;
    /* Index-driven translate, not framer's layout animation (which measures on
       every render): one composited property. Each segment is (100% of the
       indicator + its 8px of inset) apart. */
    transform: ${({ $index }) => `translateX(calc(${$index} * (100% + 8px)))`};
    transition: transform ${minimalVars.motion.standard} ${minimalVars.motion.easeStandard};

    @media (prefers-reduced-motion: reduce) {
      transition: none;
    }
    width: ${({ $count }) => `calc((100% / ${$count}) - 8px)`};
    background: ${({ $variant }) => ($variant === "brand" ? minimalVars.color.brand : minimalVars.color.bgSurface)};
    border: 1px solid ${({ $variant }) => ($variant === "brand" ? "transparent" : minimalVars.color.borderStrong)};
    border-radius: ${minimalVars.radius.sm};
    box-shadow: ${({ $variant }) => ($variant === "brand" ? "none" : minimalVars.shadow.subtle)};
  `,
  SegmentedButton: styled.button<{ $selected: boolean; $size: MinimalSize; $count: number; $variant: "neutral" | "brand" }>`
    ${clickableReset}
    ${focusRing}
    color: ${({ $selected, $variant }) =>
      $selected
        ? $variant === "brand"
          ? minimalVars.color.textInverse
          : minimalVars.color.textPrimary
        : minimalVars.color.textSecondary};
    cursor: pointer;
    position: relative;
    z-index: 1;
    min-width: 72px;
    width: ${({ $count }) => `${100 / $count}%`};
    padding: ${({ $size }) => ($size === "sm" ? "6px 10px" : $size === "lg" ? "10px 14px" : "8px 12px")};
    letter-spacing: 0;
    line-height: 1;
    font-size: ${({ $size }) => sizeFont[$size]};
    font-weight: ${minimalVars.typography.weightSemibold};

    &:disabled {
      cursor: not-allowed;
      opacity: 0.5;
    }
  `,
  ExplainerShell: styled.div`
    border: 1px solid ${minimalVars.color.borderSubtle};
    border-radius: ${minimalVars.radius.md};
    background: ${minimalVars.color.bgSurfaceAlt};
    overflow: hidden;
  `,
  ExplainerToggle: styled.button`
    ${clickableReset}
    ${focusRing}
    width: 100%;
    color: ${minimalVars.color.textPrimary};
    display: flex;
    align-items: center;
    gap: ${minimalVars.space.xs};
    justify-content: space-between;
    padding: ${minimalVars.space.sm};
    cursor: pointer;
  `,
  ExplainerCopy: styled.div`
    display: flex;
    align-items: center;
    gap: ${minimalVars.space.xs};
    min-width: 0;
  `,
  ExplainerText: styled.div`
    display: grid;
    gap: ${minimalVars.space["2xs"]};
    text-align: left;
  `,
  ExplainerActions: styled.div`
    display: flex;
    align-items: center;
    gap: ${minimalVars.space.xs};
  `,
  ExplainerPanel: styled.div`
    display: grid;
    grid-template-rows: 0fr;
    opacity: 0;
    /* Height through the grid track, not framer's per-frame height writes: the
       engine interpolates the row itself. Still layout work, kept to this one
       small panel; an engine that cannot interpolate tracks changes instantly. */
    transition:
      grid-template-rows ${minimalVars.motion.standard} ${minimalVars.motion.easeStandard},
      opacity ${minimalVars.motion.standard} ${minimalVars.motion.easeStandard};

    &[data-state="open"] {
      grid-template-rows: 1fr;
      opacity: 1;
    }

    @media (prefers-reduced-motion: reduce) {
      transition: none;
    }
  `,
  ExplainerPanelClip: styled.div`
    min-height: 0;
    overflow: hidden;
  `,
  ExplainerPanelBody: styled.div`
    padding: 0 ${minimalVars.space.sm} ${minimalVars.space.sm};
  `,
  StatShell: styled.article`
    ${styleBlock10}
    border-radius: ${minimalVars.radius.md};
    display: grid;
    gap: ${minimalVars.space["2xs"]};
    min-height: 0;
    padding: 20px;
  `,
  StatMeta: styled.div`
    display: flex;
    align-items: center;
    gap: ${minimalVars.space.xs};
  `,
  StatIcon: styled.div`
    width: 36px;
    height: 36px;
    border-radius: ${minimalVars.radius.sm};
    background: var(--minimal-stat-bg);
    color: var(--minimal-stat-accent);
    display: inline-flex;
    align-items: center;
    justify-content: center;
  `,
  StatLabel: styled.span`
    color: ${minimalVars.color.textTertiary};
    font-size: ${minimalVars.typography.metaSize};
    font-weight: ${minimalVars.typography.weightSemibold};
    letter-spacing: 0.08em;
    text-transform: uppercase;
  `,
  StatValue: styled.strong`
    color: ${minimalVars.color.textPrimary};
    font-size: 1.625rem;
    font-weight: 300;
    line-height: ${minimalVars.typography.lineHeightTight};
    letter-spacing: 0;
    font-variant-numeric: tabular-nums;
  `,
  StatHint: styled.p`
    margin: 0;
    color: var(--minimal-stat-accent);
    font-size: ${minimalVars.typography.captionSize};
  `,
  StatTitle: styled.p`
    margin: 0;
    color: ${minimalVars.color.textSecondary};
    font-size: ${minimalVars.typography.bodySize};
    line-height: ${minimalVars.typography.lineHeightBody};
  `,
  FormSectionShell: styled.section`
    display: grid;
    gap: ${minimalVars.space.sm};
  `,
  FormSectionHeader: styled.div`
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: ${minimalVars.space.sm};
  `,
  FormSectionCopy: styled.div`
    display: grid;
    gap: ${minimalVars.space["2xs"]};
  `,
  FormSectionTitle: styled.h3`
    margin: 0;
    color: ${minimalVars.color.textPrimary};
    font-family: ${minimalVars.typography.displayFamily};
    font-size: 1.125rem;
    line-height: ${minimalVars.typography.lineHeightTight};
    letter-spacing: 0;
  `,
  FieldGridShell: styled.div<{ $columns: 1 | 2 | 3 }>`
    display: grid;
    gap: ${minimalVars.space.sm};
    /* Mobile-first: one column is the base state, columns are what a wider
       viewport adds. The rule it replaces read the other way round and
       switched at a hand-typed 800px, a number no other rule in the system
       shared. */
    grid-template-columns: 1fr;

    ${from("page")} {
      grid-template-columns: repeat(${({ $columns }) => $columns}, minmax(0, 1fr));
    }
  `,
  StackShell: styled.div<{ $rhythm: MinimalSpaceStep }>`
    /* Flow layout, deliberately. Not \`display: flex; flex-direction: column\`
       — margins do not collapse in flex, and collapsing is what lets a nested
       claim resolve against its parent's instead of adding to it. A column that
       genuinely needs \`align-items\`, \`order\` or \`flex\` on a child is not
       document-shaped and should keep its gap. */
    display: block;

    /* One direction only. Exactly one element owns each vertical space, so
       there is never a trailing margin pushing the container and never a
       \`:last-child\` reset to remember. */
    > * + * {
      margin-block-start: ${({ $rhythm }) => minimalVars.space[$rhythm]};
    }

    /* How a child claims more than the default. This is the part \`gap\` cannot
       express: the claim is written on the element that needs it, so it travels
       when the element moves and applies the moment another one is added. */
    ${styleBlock11}

    /* Nothing claims space above the first child; the container's own padding
       owns that edge. */
    > :first-child {
      margin-block-start: 0;
    }
  `,
  ActionRowShell: styled.div<{ $align: ActionAlign }>`
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: ${minimalVars.space.xs};
    justify-content: ${({ $align }) => actionJustify[$align]};
  `,
  TableShell: styled.div`
    overflow-x: auto;
    border-radius: ${minimalVars.radius.md};
    border: 1px solid ${minimalVars.color.borderSubtle};
    background: ${minimalVars.color.bgSurface};
  `,
  StyledTable: styled.table<{ $density: MinimalDensity }>`
    width: 100%;
    border-collapse: separate;
    border-spacing: 0;
    font-size: ${minimalVars.typography.captionSize};

    caption {
      caption-side: top;
      padding: ${minimalVars.space.sm};
      text-align: left;
      color: ${minimalVars.color.textSecondary};
      font-size: ${minimalVars.typography.captionSize};
    }

    th,
    td {
      padding: ${({ $density }) => densityPadding[$density]};
      border-bottom: 1px solid ${minimalVars.color.borderSubtle};
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
        background: ${minimalVars.color.bgSurfaceHover};
      }
    }
  `,
  TableHeaderCell: styled.th<{ $align: "left" | "center" | "right"; $width?: string }>`
    width: ${({ $width }) => $width ?? "auto"};
    text-align: ${({ $align }) => $align};
    background: ${minimalVars.color.bgSurface};
    color: ${minimalVars.color.textSecondary};
    font-family: ${minimalVars.typography.bodyFamily};
    font-size: ${minimalVars.typography.metaSize};
    font-weight: ${minimalVars.typography.weightSemibold};
    letter-spacing: 0.04em;
    text-transform: uppercase;
  `,
  TableCell: styled.td<{ $align: "left" | "center" | "right" }>`
    text-align: ${({ $align }) => $align};
  `,
  CalendarShell: styled.section`
    display: grid;
    gap: ${minimalVars.space.sm};
    width: 100%;
    min-width: 0;
    box-sizing: border-box;
    border: 1px solid ${minimalVars.color.borderSubtle};
    border-radius: ${minimalVars.radius.md};
    background: ${minimalVars.color.bgSurface};
    padding: ${minimalVars.space.sm};
  `,
  CalendarHeader: styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: ${minimalVars.space.xs};
  `,
  CalendarTitleButton: styled.button`
    ${clickableReset}
    ${focusRing}
    min-width: 0;
    min-height: var(--minimal-control-min-target);
    display: inline-flex;
    align-items: center;
    gap: ${minimalVars.space["2xs"]};
    padding: 4px 8px;
    border-radius: ${minimalVars.radius.sm};
    color: ${minimalVars.color.textPrimary};
    font-weight: ${minimalVars.typography.weightSemibold};
    cursor: pointer;

    &:disabled {
      cursor: default;
    }

    @media (hover: hover) and (pointer: fine) {
      &:not(:disabled):hover {
        background: ${minimalVars.color.bgSurfaceHover};
      }
    }
  `,
  CalendarNavGroup: styled.div`
    display: inline-flex;
    align-items: center;
    gap: ${minimalVars.space["3xs"]};
  `,
  CalendarNavButton: styled.button`
    ${clickableReset}
    ${focusRing}
    color: ${minimalVars.color.textSecondary};
    border: 1px solid transparent;
    border-radius: ${minimalVars.radius.sm};
    width: var(--minimal-control-min-target);
    height: var(--minimal-control-min-target);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;

    &:disabled {
      cursor: not-allowed;
      opacity: 0.42;
    }

    @media (hover: hover) and (pointer: fine) {
      &:not(:disabled):hover {
        color: ${minimalVars.color.textPrimary};
        border-color: ${minimalVars.color.borderSubtle};
        background: ${minimalVars.color.bgSurfaceHover};
      }
    }
  `,
  CalendarViewport: styled.div`
    position: relative;
    min-height: 300px;
    overflow: hidden;
  `,
  CalendarViewPanel: styled.div`
    ${minimalEnter.slideX}
    position: absolute;
    inset: 0;
    width: 100%;
  `,
  CalendarGrid: styled.div`
    display: grid;
    grid-template-columns: repeat(7, minmax(0, 1fr));
    gap: ${minimalVars.space["3xs"]};
  `,
  CalendarWeekday: styled.div`
    color: ${minimalVars.color.textTertiary};
    font-size: ${minimalVars.typography.metaSize};
    font-family: ${minimalVars.typography.monoFamily};
    text-align: center;
    text-transform: uppercase;
    padding: 4px 0 ${minimalVars.space["2xs"]};
  `,
  CalendarBlank: styled.span`
    min-height: 40px;
  `,
  CalendarDay: styled.button<{
    $selected: boolean;
    $hasContent: boolean;
  }>`
    ${clickableReset}
    ${focusRing}
    ${styleBlock12}
    ${styleBlock13}
    position: relative;
    isolation: isolate;
    min-width: 0;
    min-height: ${({ $hasContent }) => ($hasContent ? "52px" : "40px")};
    aspect-ratio: ${({ $hasContent }) => ($hasContent ? "auto" : "1")};
    border-radius: ${minimalVars.radius.sm};
    padding: ${({ $hasContent }) => ($hasContent ? minimalVars.space.xs : minimalVars.space["2xs"])};
    display: grid;
    place-items: center;
    align-content: ${({ $hasContent }) => ($hasContent ? "start" : "center")};
    gap: ${minimalVars.space["2xs"]};
    cursor: pointer;
    transition:
      border-color 160ms ${enterCurve},
      background-color 160ms ${enterCurve},
      color 160ms ${enterCurve};

    ${styleBlock14}

    @media (hover: hover) and (pointer: fine) {
      &:not([aria-disabled="true"]):hover {
        border-color: ${({ $selected }) => ($selected ? minimalVars.color.brand : minimalVars.color.borderStrong)};
        background: ${({ $selected }) => ($selected ? minimalVars.color.brand : minimalVars.color.bgSurfaceAlt)};
      }
    }
  `,
  CalendarSelection: styled.span`
    ${minimalEnter.pop}
    position: absolute;
    inset: 0;
    z-index: -1;
    border-radius: inherit;
    background: ${minimalVars.color.brand};
    box-shadow: ${minimalVars.shadow.subtle};
  `,
  CalendarDayContent: styled.span`
    position: relative;
    z-index: 1;
    display: contents;
  `,
  CalendarSelectorGrid: styled.div<{ $columns: number }>`
    display: grid;
    grid-template-columns: repeat(${({ $columns }) => $columns}, minmax(0, 1fr));
    gap: ${minimalVars.space["2xs"]};
    padding-top: ${minimalVars.space["2xs"]};
  `,
  CalendarOption: styled.button<{ $active: boolean }>`
    ${clickableReset}
    ${focusRing}
    min-height: var(--minimal-control-min-target);
    padding: 8px;
    border: 1px solid ${({ $active }) => ($active ? minimalVars.color.brand : minimalVars.color.borderSubtle)};
    border-radius: ${minimalVars.radius.sm};
    background: ${({ $active }) => ($active ? minimalVars.color.brand : minimalVars.color.bgSurface)};
    color: ${({ $active }) => ($active ? minimalVars.color.textInverse : minimalVars.color.textPrimary)};
    cursor: pointer;
    font-size: ${minimalVars.typography.captionSize};
    font-weight: ${({ $active }) =>
      $active ? minimalVars.typography.weightSemibold : minimalVars.typography.weightMedium};

    &:disabled {
      cursor: not-allowed;
      opacity: 0.42;
    }

    @media (hover: hover) and (pointer: fine) {
      &:not(:disabled):hover {
        border-color: ${minimalVars.color.brand};
        background: ${({ $active }) => ($active ? minimalVars.color.brand : minimalVars.color.brandSoft)};
      }
    }
  `,
  CalendarFooter: styled.div`
    display: flex;
    justify-content: flex-start;
    padding-top: ${minimalVars.space["2xs"]};
    border-top: 1px solid ${minimalVars.color.borderSubtle};
  `,
  CalendarTodayButton: styled.button`
    ${clickableReset}
    ${focusRing}
    min-height: var(--minimal-control-min-target);
    padding: 6px 8px;
    border-radius: ${minimalVars.radius.sm};
    color: ${minimalVars.color.brand};
    cursor: pointer;
    font-size: ${minimalVars.typography.captionSize};
    font-weight: ${minimalVars.typography.weightSemibold};

    &:disabled {
      cursor: not-allowed;
      color: ${minimalVars.color.textTertiary};
    }
  `,
  FloatingPanelContainer: styled.div<{
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
    z-index: ${minimalVars.zIndex.dropdown};
    transform: ${({ $placement }) => ($placement === "top" ? "translateY(-100%)" : "none")};
    display: flex;
    flex-direction: column;
  `,
  FloatingPanel: styled.div`
    ${minimalEnter.pop}
    display: flex;
    flex-direction: column;
    width: 100%;
    max-height: inherit;
    min-height: 0;
    background: ${minimalVars.color.bgSurface};
    border: 1px solid ${minimalVars.color.borderStrong};
    border-radius: ${minimalVars.radius.md};
    box-shadow: ${minimalVars.shadow.floating};
    overflow: hidden;
  `,
  DropdownTriggerButton: styled.button<{ $placeholder: boolean }>`
    ${clickableReset}
    ${focusRing}
    width: 100%;
    align-items: center;
    color: ${({ $placeholder }) => ($placeholder ? minimalVars.color.textTertiary : minimalVars.color.textPrimary)};
    display: inline-flex;
    gap: ${minimalVars.space.xs};
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
    flex: 1 1 auto;
    min-height: 0;
    overflow-y: auto;
    overscroll-behavior: contain;
    padding: ${minimalVars.space["2xs"]};
  `,
  DropdownSearchWrap: styled.div`
    flex: 0 0 auto;
    padding: ${minimalVars.space.xs} ${minimalVars.space.xs} ${minimalVars.space["2xs"]};
    border-bottom: 1px solid ${minimalVars.color.borderSubtle};
  `,
  DropdownSearch: styled.input`
    width: 100%;
    border: 1px solid ${minimalVars.color.borderSubtle};
    border-radius: ${minimalVars.radius.sm};
    background: ${minimalVars.color.bgSurfaceAlt};
    padding: 10px 12px;
    font: inherit;
    color: ${minimalVars.color.textPrimary};
    outline: none;
    transition:
      border-color 160ms ${enterCurve},
      box-shadow 160ms ${enterCurve};

    &:focus {
      border-color: ${minimalVars.color.borderFocus};
      box-shadow: 0 0 0 ${minimalVars.focus.ringWidth} ${minimalVars.color.brandSoft};
    }
  `,
  DropdownEmptyState: styled.div`
    padding: 10px 12px;
    color: ${minimalVars.color.textSecondary};
    font-size: ${minimalVars.typography.captionSize};
    line-height: ${minimalVars.typography.lineHeightBody};
  `,
  DropdownOptionButton: styled.button<{ $selected: boolean; $active?: boolean }>`
    ${clickableReset}
    ${focusRing}
    width: 100%;
    text-align: left;
    display: grid;
    gap: ${minimalVars.space["3xs"]};
    padding: 10px 12px;
    border-radius: ${minimalVars.radius.sm};
    cursor: pointer;
    background: ${({ $selected, $active }) => ($selected || $active ? minimalVars.color.bgSurfaceAlt : "transparent")};
    color: ${minimalVars.color.textPrimary};
    transition: background-color 160ms ${enterCurve}, color 160ms ${enterCurve};

    @media (hover: hover) and (pointer: fine) {
      &:hover {
        background: ${minimalVars.color.bgSurfaceAlt};
      }
    }

    &:disabled {
      cursor: not-allowed;
      color: ${minimalVars.color.textTertiary};
    }
  `,
  DropdownOptionRow: styled.div`
    display: flex;
    justify-content: space-between;
    gap: ${minimalVars.space.sm};
  `,
  TooltipAnchor: styled.span`
    display: inline-flex;
  `,
  TooltipPanel: styled.div<{
    $maxWidth: string;
    $top: number;
    $left: number;
    $placement: TooltipPlacement;
  }>`
    position: fixed;
    top: ${({ $top }) => `${$top}px`};
    left: ${({ $left }) => `${$left}px`};
    max-width: ${({ $maxWidth }) => $maxWidth};
    background: ${minimalVars.color.textPrimary};
    color: ${minimalVars.color.textInverse};
    border-radius: ${minimalVars.radius.sm};
    padding: 8px 10px;
    box-shadow: ${minimalVars.shadow.medium};
    z-index: ${minimalVars.zIndex.tooltip};
    font-size: ${minimalVars.typography.captionSize};
    line-height: ${minimalVars.typography.lineHeightBody};
    pointer-events: none;
    transform: ${({ $placement }) =>
      $placement === "top" ? "translate(-50%, -100%)" : "translate(-50%, 0)"};
    ${minimalEnter.tooltip}
  `,
  ModalBackdrop: styled.div`
    ${minimalEnter.fade}
    position: fixed;
    inset: 0;
    background: ${minimalVars.color.bgOverlay};
    /* Tier-controlled (theme.tsx): none on low_power and reduced_motion. */
    backdrop-filter: var(--minimal-backdrop-filter, blur(4px));
    z-index: ${minimalVars.zIndex.overlay};
  `,
  ModalShell: styled.section<{ $mobileSheet: boolean }>`
    ${minimalEnter.pop}
    position: fixed;
    inset: 50% auto auto 50%;
    transform: translate(-50%, -50%);
    width: min(92vw, var(--minimal-modal-max-width, 520px));
    max-height: var(--minimal-modal-max-height, calc(100dvh - 48px));
    background: ${minimalVars.color.bgSurface};
    border: 1px solid ${minimalVars.color.borderStrong};
    border-radius: ${minimalVars.radius.md};
    box-shadow: ${minimalVars.shadow.floating};
    padding: ${minimalVars.space.md};
    z-index: ${minimalVars.zIndex.modal};
    display: grid;
    gap: ${minimalVars.space.sm};
    overflow: hidden;

    ${until("page")} {
      inset: ${({ $mobileSheet }) => ($mobileSheet ? "auto 0 0 0" : "50% auto auto 50%")};
      transform: ${({ $mobileSheet }) => ($mobileSheet ? "none" : "translate(-50%, -50%)")};
      width: ${({ $mobileSheet }) => ($mobileSheet ? "100%" : "min(92vw, var(--minimal-modal-max-width, 520px))")};
      max-height: min(90dvh, var(--minimal-modal-max-height, 90dvh));
      border-radius: ${({ $mobileSheet }) =>
        $mobileSheet ? `${minimalVars.radius.lg} ${minimalVars.radius.lg} 0 0` : minimalVars.radius.md};
      padding: ${`${minimalVars.space.md} ${minimalVars.space.sm}`};

      ${styleBlock15}
    }
  `,
  ModalHeader: styled.div`
    display: grid;
    gap: ${minimalVars.space.xs};
  `,
  ModalTitle: styled.h2`
    margin: 0;
    color: ${minimalVars.color.textPrimary};
    font-family: ${minimalVars.typography.displayFamily};
    font-size: ${minimalVars.typography.h1Size};
    line-height: ${minimalVars.typography.lineHeightTight};
    letter-spacing: 0;
  `,
  ModalActions: styled.div`
    display: flex;
    justify-content: flex-end;
    gap: ${minimalVars.space.xs};
    flex-wrap: wrap;
  `,
  ModalBody: styled.div<{ $scrollable: boolean }>`
    min-height: 0;
    overflow-y: ${({ $scrollable }) => ($scrollable ? "auto" : "visible")};
    padding-right: ${({ $scrollable }) => ($scrollable ? "4px" : "0")};
  `,
  DisplaySection: styled.section<{
    $anchor: LandingAnchor;
    $intensity: LandingIntensity;
    $minHeight: string;
    $backgroundImage?: string;
    $overlay?: string;
  }>`
    ${minimalEnter.slideUp}
    position: relative;
    isolation: isolate;
    display: grid;
    align-items: ${({ $anchor }) => ($anchor === "bottom-left" || $anchor === "bottom-right" ? "end" : "center")};
    min-height: ${({ $minHeight }) => $minHeight};
    overflow: hidden;
    border-radius: ${minimalVars.radius.md};
    border: 1px solid ${minimalVars.color.borderSubtle};
    background:
      ${({ $overlay }) =>
        $overlay ??
        `linear-gradient(180deg, color-mix(in srgb, ${minimalVars.color.bgSurface} 72%, transparent), color-mix(in srgb, ${minimalVars.color.bgSurface} 92%, transparent))`},
      ${({ $backgroundImage }) => ($backgroundImage ? `url(${$backgroundImage}) center / cover` : "transparent")};
    box-shadow: ${({ $intensity }) => ($intensity === "statement" ? minimalVars.shadow.medium : minimalVars.shadow.subtle)};
    padding: ${({ $intensity }) =>
      $intensity === "statement" ? minimalVars.space.lg : $intensity === "calm" ? minimalVars.space.md : minimalVars.space.lg};

    ${styleBlock16}
    /* Every other mode — including one the union does not name — gets the
       split layout, as the else-branch this replaced did. The negation sits
       inside :where() so it adds no specificity, and is chained rather than
       comma-listed so no selector splitter can misread it. */
    &:where(:not([data-minimal-visual-mode="background"]):not([data-minimal-visual-mode="canvas"])) {
      grid-template-columns: minmax(0, 1fr);
      gap: ${minimalVars.space.lg};
      background-color: ${minimalVars.color.bgSurface};
      ${styleBlock17}
      ${styleBlock18}
    }

    ${until("page")} {
      grid-template-columns: 1fr;
      min-height: min(760px, max(520px, 76dvh));
      padding: ${minimalVars.space.md};
    }
  `,
  DisplayCopy: styled.div<{ $anchor: LandingAnchor; $intensity: LandingIntensity }>`
    position: relative;
    z-index: 1;
    display: grid;
    gap: ${minimalVars.space.sm};
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
    color: ${minimalVars.color.textPrimary};
    font-family: ${minimalVars.typography.displayFamily};
    font-size: ${({ $intensity }) =>
      $intensity === "statement" ? minimalVars.typography.displaySize : minimalVars.typography.h1Size};
    line-height: ${minimalVars.typography.lineHeightTight};
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
    border-radius: ${minimalVars.radius.md};

    > * {
      width: 100%;
      height: 100%;
    }

    ${until("page")} {
      order: 0;
      max-height: 360px;
    }
  `,
  LandingSection: styled.section<{ $anchor: LandingAnchor; $intensity: LandingIntensity }>`
    display: grid;
    gap: ${({ $intensity }) => ($intensity === "calm" ? minimalVars.space.sm : minimalVars.space.md)};
    padding: ${({ $intensity }) =>
      $intensity === "statement" ? `${minimalVars.space.xl} 0` : `${minimalVars.space.lg} 0`};
    align-items: center;
    grid-template-columns: ${({ $anchor }) =>
      $anchor === "left-visual"
        ? "minmax(320px, 1fr) minmax(0, 0.9fr)"
        : $anchor === "right-visual"
          ? "minmax(0, 0.9fr) minmax(320px, 1fr)"
          : "minmax(0, 1fr)"};

    ${until("page")} {
      grid-template-columns: 1fr;
      padding: ${`${minimalVars.space.lg} 0`};
    }
  `,
  LandingCopy: styled.div<{ $anchor: LandingAnchor }>`
    display: grid;
    gap: ${minimalVars.space.xs};
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
    border-radius: ${minimalVars.radius.md};
    border: 1px solid ${minimalVars.color.borderSubtle};
    background: ${minimalVars.color.bgSurfaceAlt};

    > * {
      width: 100%;
      height: 100%;
    }
  `,
  InfoPanel: styled.article<{ $layout: InfoLayout }>`
    ${styleBlock19}
    display: grid;
    grid-template-columns: ${({ $layout }) =>
      $layout === "row" ? "auto minmax(0, 1fr) auto" : $layout === "split" ? "minmax(0, 1fr) auto" : "1fr"};
    gap: ${minimalVars.space.sm};
    align-items: start;
    min-width: 0;
    padding: ${minimalVars.space.sm};
    border: 1px solid ${minimalVars.color.borderSubtle};
    border-left: 2px solid var(--minimal-info-accent);
    border-radius: ${minimalVars.radius.sm};
    background: ${minimalVars.color.bgSurface};

    ${until("page")} {
      grid-template-columns: 1fr;
    }
  `,
  InfoIcon: styled.div`
    width: 36px;
    height: 36px;
    border-radius: ${minimalVars.radius.sm};
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: var(--minimal-info-bg);
    color: var(--minimal-info-accent);
  `,
  InfoCopy: styled.div`
    display: grid;
    gap: ${minimalVars.space["2xs"]};
    min-width: 0;
  `,
  InfoTitle: styled.h3`
    margin: 0;
    color: ${minimalVars.color.textPrimary};
    font-size: ${minimalVars.typography.h2Size};
    line-height: ${minimalVars.typography.lineHeightTight};
  `,
  Skeleton: styled.span<{
    $width?: string;
    $height?: string;
    $inline: boolean;
    $radius?: string;
  }>`
    @keyframes minimal-skeleton-sweep {
      0% {
        transform: translateX(-100%);
      }
      100% {
        transform: translateX(100%);
      }
    }
    position: relative;
    overflow: hidden;
    display: ${({ $inline }) => ($inline ? "inline-flex" : "block")};
    width: ${({ $width }) => $width ?? "100%"};
    height: ${({ $height }) => $height ?? "1rem"};
    border-radius: ${({ $radius }) => $radius ?? minimalVars.radius.md};
    background: ${minimalVars.color.bgSurfaceAlt};

    /* The highlight is its own box so the sweep is a transform the compositor
       runs alone; the placeholder underneath never restyles or repaints. */
    &::after {
      content: "";
      position: absolute;
      inset: 0;
      background: linear-gradient(90deg, transparent 0%, ${minimalVars.color.bgSurface} 50%, transparent 100%);
      transform: translateX(-100%);
      animation: minimal-skeleton-sweep 1.2s linear infinite;
    }

    /* Off screen: paused, not removed, so it resumes mid-sweep with no restart. */
    &[data-minimal-offscreen]::after {
      animation-play-state: paused;
    }

    @media (prefers-reduced-motion: reduce) {
      &::after {
        animation: none;
      }
    }

    /* P3 rule 4: an infinite loop spends a frame forever. Where the quality
       tier says spend less, the placeholder holds still; it still reads as a
       placeholder without the sweep. */
    :root:where([data-ui-tier="low_power"], [data-ui-tier="reduced_motion"]) &::after {
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
  ExplainerPanelClip,
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
  StackShell,
  ActionRowShell,
  TableShell,
  StyledTable,
  TableHeaderCell,
  TableCell,
  CalendarShell,
  CalendarHeader,
  CalendarTitleButton,
  CalendarNavGroup,
  CalendarNavButton,
  CalendarViewport,
  CalendarViewPanel,
  CalendarGrid,
  CalendarWeekday,
  CalendarBlank,
  CalendarDay,
  CalendarSelection,
  CalendarDayContent,
  CalendarSelectorGrid,
  CalendarOption,
  CalendarFooter,
  CalendarTodayButton,
  FloatingPanel,
  FloatingPanelContainer,
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

/*
 * A plain class, not a styled component: every attribute (loading, decoding,
 * fetchpriority, srcset, sizes) must reach the <img> untouched.
 */
const imageClass = css`
  display: block;
  max-width: 100%;
  height: auto;
  /* The reserved box reads as a placeholder until pixels arrive. */
  background: ${minimalVars.color.bgSurfaceAlt};

  &[data-minimal-fill] {
    width: 100%;
  }
  &[data-minimal-fit="cover"] {
    object-fit: cover;
  }
  &[data-minimal-fit="contain"] {
    object-fit: contain;
  }
  &[data-minimal-reveal] {
    transition: opacity 240ms ${minimalVars.motion.easeStandard};
  }
  &[data-minimal-reveal="pending"] {
    opacity: 0;
  }
  @media (prefers-reduced-motion: reduce) {
    &[data-minimal-reveal] {
      transition: none;
    }
  }
`;

/* A transform transition on an attribute: composited, and supported back to Safari 9. */
const ChevronSvg = styled.svg`
  transition: transform 180ms ${minimalVars.motion.easeStandard};

  &[data-open="true"] {
    transform: rotate(180deg);
  }

  @media (prefers-reduced-motion: reduce) {
    transition: none;
  }
`;

const Chevron = ({ open }: { open: boolean }) => (
  <ChevronSvg width="14" height="14" viewBox="0 0 20 20" fill="none" aria-hidden="true" data-open={open}>
    <path
      d="M5 7.5 10 12.5 15 7.5"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  </ChevronSvg>
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
  if (typeof value === "string") {
    const plainDate = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
    if (plainDate) {
      const [, year, month, day] = plainDate;
      const localDate = new Date(Number(year), Number(month) - 1, Number(day));
      return localDate.getFullYear() === Number(year) &&
        localDate.getMonth() === Number(month) - 1 &&
        localDate.getDate() === Number(day)
        ? localDate
        : null;
    }
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

const useFocusTrap = (ref: RefObject<HTMLElement | null>, enabled: boolean) => {
  useEffect(() => {
    if (!enabled || !isBrowser() || !ref.current) {
      return;
    }

    const previousActiveElement = document.activeElement as HTMLElement | null;
    const element = ref.current;

    const focusableSelectors = [
      "a[href]",
      "area[href]",
      "input:not([disabled])",
      "select:not([disabled])",
      "textarea:not([disabled])",
      "button:not([disabled])",
      "iframe",
      "object",
      "embed",
      "[contenteditable]",
      '[tabindex]:not([tabindex^="-"])',
    ];

    const getFocusableElements = (): HTMLElement[] => {
      if (!element) return [];
      const list = Array.from(element.querySelectorAll<HTMLElement>(focusableSelectors.join(",")));
      return list.filter((el) => {
        const style = window.getComputedStyle(el);
        return el.tabIndex >= 0 && style.display !== "none" && style.visibility !== "hidden";
      });
    };

    const focusables = getFocusableElements();
    if (focusables.length > 0) {
      focusables[0].focus();
    } else {
      element.focus();
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Tab") {
        return;
      }

      const currentFocusables = getFocusableElements();
      if (currentFocusables.length === 0) {
        event.preventDefault();
        return;
      }

      const first = currentFocusables[0];
      const last = currentFocusables[currentFocusables.length - 1];
      const active = document.activeElement as HTMLElement | null;

      if (event.shiftKey) {
        if (active === first || (active && !element.contains(active))) {
          last.focus();
          event.preventDefault();
        }
      } else {
        if (active === last || (active && !element.contains(active))) {
          first.focus();
          event.preventDefault();
        }
      }
    };

    document.addEventListener("keydown", handleKeyDown);

    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      if (previousActiveElement && typeof previousActiveElement.focus === "function") {
        previousActiveElement.focus();
      }
    };
  }, [enabled, ref]);
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

      const next: FloatingPosition = {
        top: placement === "bottom" ? rect.bottom + floatingYOffset : rect.top - floatingYOffset,
        left,
        width,
        maxHeight: Math.min(panelHeight, availableHeight),
        placement,
      };
      // Scroll fires in capture phase for every scrollable (including the
      // panel's own option list); only commit real moves so an open panel
      // isn't re-rendered on every scrolled frame.
      setPosition((current) =>
        current &&
        current.top === next.top &&
        current.left === next.left &&
        current.width === next.width &&
        current.maxHeight === next.maxHeight &&
        current.placement === next.placement
          ? current
          : next,
      );
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

/* Build-time style block (Linaria evaluates it once). */
const styleBlock20 = variantRules("mobile", [true] as const, () => `
      max-width: 480px;
      width: 100%;
      margin: 0 auto;
      box-shadow: ${minimalVars.shadow.floating};
      position: relative;
      overflow-x: hidden;
    `);
const ShellStyle = {
  App: styled.div<{ $mobile?: boolean }>`
    min-height: 100dvh;
    display: flex;
    flex-direction: ${({ $mobile }) => ($mobile ? "column" : "row")};
    isolation: isolate;
    background: ${minimalVars.color.bgApp};
    color: ${minimalVars.color.textPrimary};

    ${styleBlock20}
  `,
  SkipLink: styled.a`
    position: absolute;
    top: -44px;
    left: ${minimalVars.space.sm};
    z-index: calc(${minimalVars.zIndex.tooltip} + 1);
    padding: ${`${minimalVars.space.xs} ${minimalVars.space.sm}`};
    border-radius: ${minimalVars.radius.md};
    background: ${minimalVars.color.brand};
    color: ${minimalVars.color.textInverse};
    font-size: ${minimalVars.typography.metaSize};
    font-weight: ${minimalVars.typography.weightSemibold};
    text-decoration: none;
    transition: top 160ms ${enterCurve};

    &:focus {
      top: ${minimalVars.space.xs};
      outline: 2px solid ${minimalVars.color.borderFocus};
      outline-offset: 2px;
    }
  `,
  Sidebar: styled.aside<{ $width: string; $bannerOffset: string }>`
    position: fixed;
    inset: 0 auto 0 0;
    width: ${({ $width }) => $width};
    display: flex;
    flex-direction: column;
    /* Deliberate gap on a column — see styling_design_practices.md §11. A
       sidebar is a scrolling flex column of uniform navigation peers: it needs
       flex for the scroll region to size against the fixed inset, and the
       spacing between its items genuinely is uniform. Rule 3, not rule 4. */
    gap: ${minimalVars.space.sm};
    padding: ${({ $bannerOffset }) => `calc(${$bannerOffset} + ${minimalVars.space.lg}) ${minimalVars.space.sm} ${minimalVars.space.md}`};
    overflow-y: auto;
    overscroll-behavior: contain;
    background: linear-gradient(180deg, ${minimalVars.color.bgSurface} 0%, ${minimalVars.color.bgSurfaceAlt} 100%);
    border-right: 1px solid ${minimalVars.color.borderSubtle};
    box-shadow: ${minimalVars.shadow.subtle};
    z-index: ${minimalVars.zIndex.sticky};
    transform: translateZ(0);
  `,
  Main: styled.main<{ $sidebarWidth: string; $bannerOffset: string; $mobile: boolean; $compact: boolean }>`
    flex: 1;
    min-height: 100dvh;
    height: 100dvh;
    width: 100%;
    max-width: ${({ $mobile, $sidebarWidth }) => ($mobile ? "100%" : `calc(100% - ${$sidebarWidth})`)};
    margin-left: ${({ $mobile, $sidebarWidth }) => ($mobile ? "0" : $sidebarWidth)};
    padding-top: ${({ $mobile, $bannerOffset }) =>
      $mobile ? `calc(${$bannerOffset} + env(safe-area-inset-top, 0px))` : `calc(${$bannerOffset} + ${minimalVars.space.lg})`};
    padding-right: ${({ $mobile, $compact }) => ($mobile && $compact ? minimalVars.space.xs : minimalVars.space.md)};
    padding-bottom: ${({ $mobile, $compact }) =>
      $mobile ? `calc(${$compact ? "64px" : "72px"} + env(safe-area-inset-bottom, 0px))` : minimalVars.space.lg};
    padding-left: ${({ $mobile, $compact }) => ($mobile && $compact ? minimalVars.space.xs : minimalVars.space.md)};
    overflow-y: auto;
    overscroll-behavior: contain;
    -webkit-overflow-scrolling: touch;
    background: ${minimalVars.color.bgApp};
    transition:
      padding 240ms ${moveCurve},
      margin-left 240ms ${moveCurve},
      background-color 240ms ${enterCurve};
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
  <ShellStyle.App data-minimal="AppShell" data-minimal-mobile={mobile} $mobile={mobile} {...props}>
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

export const MinimalHeader = ({
  kicker,
  title,
  subtitle,
  description,
  meta,
  actions,
  align = "start",
  titleAs = "h1",
  enter = true,
  children,
  ...props
}: MinimalHeaderProps) => {
  return (
    <HeaderShell
      data-minimal="Header"
      data-minimal-enter={enter ? undefined : "false"}
      $align={align}
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
  enter = true,
  children,
  ...props
}: MinimalDisplaySectionProps) => {
  return (
    <DisplaySection
      data-minimal="DisplaySection"
      data-minimal-enter={enter ? undefined : "false"}
      data-minimal-anchor={anchor}
      data-minimal-visual-mode={visualMode}
      $anchor={anchor}
      $intensity={intensity}
      $minHeight={minHeight}
      $backgroundImage={backgroundImage}
      $overlay={overlay}
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
  <InfoPanel data-minimal="InfoPanel" data-minimal-tone={tone} $layout={layout} {...props}>
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
  // Hover lift and press are CSS on ButtonShell (with reduced-motion rules); the
  // framer whileHover/whileTap that used to sit on top of them are gone.

  return (
    <ButtonShell
      data-minimal="Button"
      data-minimal-variant={variant}
      data-minimal-tone={tone}
      $variant={variant}
      $tone={tone}
      $size={size}
      $fullWidth={fullWidth}
      aria-busy={loading || undefined}
      disabled={disabled || loading}
      {...props}
    >
      {loading ? <Spinner aria-hidden="true" /> : leading}
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
}: MinimalSkeletonProps) => {
  const ref = useRef<HTMLSpanElement>(null);
  useEffect(() => (ref.current ? observeSkeleton(ref.current) : undefined), []);
  return (
    <Skeleton
      ref={ref}
      data-minimal="Skeleton"
      aria-hidden="true"
      $width={width}
      $height={height}
      $inline={inline}
      $radius={radius}
      {...props}
    />
  );
};

export const MinimalImage = forwardRef<HTMLImageElement, MinimalImageProps>(function MinimalImage(
  { width, height, aspectRatio, priority = false, fit, reveal = false, radius, loading, decoding, className, style, ...props },
  forwarded,
) {
  const local = useRef<HTMLImageElement | null>(null);
  const [revealed, setRevealed] = useState(false);
  const setRef = useCallback(
    (node: HTMLImageElement | null) => {
      local.current = node;
      if (typeof forwarded === "function") forwarded(node);
      else if (forwarded) forwarded.current = node;
    },
    [forwarded],
  );

  useEffect(() => {
    const image = local.current;
    if (!reveal || !image) return;
    let live = true;
    const show = () => {
      if (live) setRevealed(true);
    };
    // Reveal after decode, so the fade never waits on the decoder (P6 rule 2).
    const decodeThenShow = () => image.decode().then(show, show);
    if (image.complete) decodeThenShow();
    else {
      image.addEventListener("load", decodeThenShow, { once: true });
      image.addEventListener("error", show, { once: true });
    }
    return () => {
      live = false;
      image.removeEventListener("load", decodeThenShow);
      image.removeEventListener("error", show);
    };
  }, [reveal, props.src]);

  const fill = aspectRatio !== undefined;
  const boxStyle: CSSProperties | undefined =
    fill || radius ? { aspectRatio: fill ? String(aspectRatio) : undefined, borderRadius: radius, ...style } : style;
  // Lowercase: a plain attribute to every React version (fetchPriority is React 19 only).
  const fetchPriority = priority ? ({ fetchpriority: "high" } as Record<string, string>) : undefined;

  return (
    <img
      ref={setRef}
      data-minimal="Image"
      data-minimal-fill={fill ? "" : undefined}
      data-minimal-fit={fit}
      data-minimal-reveal={reveal ? (revealed ? "done" : "pending") : undefined}
      className={className ? `${imageClass} ${className}` : imageClass}
      width={width}
      height={height}
      loading={loading ?? (priority ? "eager" : "lazy")}
      decoding={decoding ?? (priority ? "auto" : "async")}
      style={boxStyle}
      {...fetchPriority}
      {...props}
    />
  );
});

export const MinimalCard = ({
  children,
  header,
  footer,
  variant = "default",
  padding = "md",
  hoverable = false,
  enter = true,
  ...props
}: MinimalCardProps) => {
  return (
    <CardShell
      data-minimal="Card"
      data-minimal-enter={enter ? undefined : "false"}
      data-minimal-variant={variant}
      data-minimal-hoverable={hoverable}
      $padding={padding}
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
      <InputFrame data-minimal-state={state} $state={state} $size={inputSize}>
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
  const [highlightedIndex, setHighlightedIndex] = useState(-1);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const frameRef = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  // Anchor the floating panel to the visible field frame, not the inner trigger
  // button (which sits inside the frame's padding). This makes the panel span
  // the select's full width and align to its bottom edge instead of being inset.
  const position = useFloatingPosition(frameRef, open, panelMaxHeight, panelMinWidth, matchTriggerWidth);
  const dismissRefs = useMemo(() => [frameRef, panelRef], []);

  useDismissLayer(dismissRefs, open, () => setOpen(false));

  const filteredOptions = useMemo(() => {
    if (!showSearch || !search.trim()) {
      return options;
    }
    const query = search.trim().toLowerCase();
    return options.filter((option) => formatSearchableText(option).includes(query));
  }, [options, search, showSearch]);

  const getNextFocusableIndex = (
    currentIndex: number,
    direction: "next" | "prev",
    optionsList: readonly MinimalOption<T>[],
  ) => {
    const len = optionsList.length;
    if (len === 0) return -1;
    const step = direction === "next" ? 1 : -1;
    let index = currentIndex;

    for (let i = 0; i < len; i++) {
      index = (index + step + len) % len;
      if (!optionsList[index].disabled) {
        return index;
      }
    }
    return -1;
  };

  useEffect(() => {
    if (open) {
      if (search) {
        const firstEnabled = filteredOptions.findIndex((opt) => !opt.disabled);
        setHighlightedIndex(firstEnabled >= 0 ? firstEnabled : 0);
      } else {
        const idx = filteredOptions.findIndex((option) => option.value === value);
        if (idx >= 0) {
          setHighlightedIndex(idx);
        } else {
          const firstEnabled = filteredOptions.findIndex((opt) => !opt.disabled);
          setHighlightedIndex(firstEnabled >= 0 ? firstEnabled : 0);
        }
      }
    } else {
      setHighlightedIndex(-1);
    }
  }, [open, search, filteredOptions, value]);

  useEffect(() => {
    if (open && showSearch) {
      const timer = setTimeout(() => {
        searchInputRef.current?.focus({ preventScroll: true });
      }, 50);
      return () => clearTimeout(timer);
    }
  }, [open, showSearch]);

  const prevOpen = useRef(open);
  useEffect(() => {
    if (!open && prevOpen.current) {
      triggerRef.current?.focus();
    }
    prevOpen.current = open;
  }, [open]);

  useEffect(() => {
    if (highlightedIndex < 0 || !panelRef.current) {
      return;
    }
    const activeEl = panelRef.current.querySelector<HTMLElement>(
      `[id="${listboxId}-option-${highlightedIndex}"]`
    );
    const listEl = activeEl?.parentElement;
    if (!activeEl || !listEl) {
      return;
    }
    // Scroll only the option list. `scrollIntoView` would also scroll the
    // overflow-hidden panel (clipping the search input out of view) and any
    // scrollable page ancestors, shifting the whole layout when the panel opens.
    const optionTop = activeEl.offsetTop - listEl.offsetTop;
    const optionBottom = optionTop + activeEl.offsetHeight;
    if (optionTop < listEl.scrollTop) {
      listEl.scrollTop = optionTop;
    } else if (optionBottom > listEl.scrollTop + listEl.clientHeight) {
      listEl.scrollTop = optionBottom - listEl.clientHeight;
    }
  }, [highlightedIndex, listboxId]);

  const handleKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (disabled) return;

    const key = event.key;
    if (!open) {
      if (key === "Enter" || key === " " || key === "ArrowDown" || key === "ArrowUp") {
        event.preventDefault();
        setOpen(true);
      }
      return;
    }

    switch (key) {
      case "ArrowDown": {
        event.preventDefault();
        const nextIndex = getNextFocusableIndex(highlightedIndex, "next", filteredOptions);
        if (nextIndex >= 0) {
          setHighlightedIndex(nextIndex);
        }
        break;
      }
      case "ArrowUp": {
        event.preventDefault();
        const prevIndex = getNextFocusableIndex(highlightedIndex, "prev", filteredOptions);
        if (prevIndex >= 0) {
          setHighlightedIndex(prevIndex);
        }
        break;
      }
      case "Enter": {
        event.preventDefault();
        const option = filteredOptions[highlightedIndex];
        if (option && !option.disabled) {
          onChange(option.value);
          setOpen(false);
          setSearch("");
        }
        break;
      }
      case " ": {
        const isSearchFocused = document.activeElement === searchInputRef.current;
        if (!isSearchFocused) {
          event.preventDefault();
          const option = filteredOptions[highlightedIndex];
          if (option && !option.disabled) {
            onChange(option.value);
            setOpen(false);
            setSearch("");
          }
        }
        break;
      }
      case "Escape": {
        event.preventDefault();
        setOpen(false);
        setSearch("");
        break;
      }
      case "Tab": {
        setOpen(false);
        setSearch("");
        break;
      }
      case "Home": {
        event.preventDefault();
        const firstEnabled = filteredOptions.findIndex((opt) => !opt.disabled);
        if (firstEnabled >= 0) {
          setHighlightedIndex(firstEnabled);
        }
        break;
      }
      case "End": {
        event.preventDefault();
        let lastEnabled = -1;
        for (let i = filteredOptions.length - 1; i >= 0; i--) {
          if (!filteredOptions[i].disabled) {
            lastEnabled = i;
            break;
          }
        }
        if (lastEnabled >= 0) {
          setHighlightedIndex(lastEnabled);
        }
        break;
      }
      default:
        break;
    }
  };

  const valueContent = renderValue ? renderValue(selectedOption) : selectedOption?.label ?? placeholder;

  return (
    <FieldShell data-minimal="Dropdown" {...props} onKeyDown={handleKeyDown}>
      {label ? <FieldLabel htmlFor={triggerId}>{label}</FieldLabel> : null}
      <InputFrame
        ref={frameRef}
        data-minimal-state={error ? "invalid" : "default"}
        $state={error ? "invalid" : "default"}
        $size="md"
      >
        <DropdownTriggerButton
          type="button"
          id={triggerId}
          ref={triggerRef}
          onClick={() => !disabled && setOpen((current) => !current)}
          disabled={disabled}
          $placeholder={!selectedOption}
          role="combobox"
          aria-expanded={open}
          aria-controls={open ? listboxId : undefined}
          aria-haspopup="listbox"
          aria-activedescendant={open && highlightedIndex >= 0 ? `${listboxId}-option-${highlightedIndex}` : undefined}
        >
          <DropdownTriggerValue>{valueContent}</DropdownTriggerValue>
          <Chevron open={open} />
        </DropdownTriggerButton>
      </InputFrame>
      {error ? <FieldMessage $tone="danger">{error}</FieldMessage> : hint ? <FieldMessage $tone="neutral">{hint}</FieldMessage> : null}
      {open && position && isBrowser()
        ? createPortal(
            <FloatingPanelContainer
              $width={position.width}
              $maxHeight={position.maxHeight}
              $placement={position.placement}
              $top={position.top}
              $left={position.left}
            >
              <FloatingPanel
                ref={panelRef}
                id={listboxId}
                role="listbox"
                aria-labelledby={label ? triggerId : undefined}
              >
                {showSearch ? (
                  <DropdownSearchWrap>
                    <DropdownSearch
                      ref={searchInputRef}
                      value={search}
                      onChange={(event) => setSearch(event.target.value)}
                      placeholder={searchPlaceholder}
                      aria-label="Filter dropdown options"
                      role="combobox"
                      aria-expanded={open}
                      aria-controls={listboxId}
                      aria-activedescendant={open && highlightedIndex >= 0 ? `${listboxId}-option-${highlightedIndex}` : undefined}
                    />
                  </DropdownSearchWrap>
                ) : null}
                <DropdownList>
                  {filteredOptions.length === 0 ? (
                    <DropdownEmptyState>No matches found.</DropdownEmptyState>
                  ) : (
                    filteredOptions.map((option, idx) => (
                      <DropdownOptionButton
                        type="button"
                        id={`${listboxId}-option-${idx}`}
                        key={option.value}
                        role="option"
                        aria-selected={option.value === value}
                        $selected={option.value === value}
                        $active={idx === highlightedIndex}
                        disabled={option.disabled}
                        tabIndex={-1}
                        onMouseEnter={() => {
                          if (!option.disabled) {
                            setHighlightedIndex(idx);
                          }
                        }}
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
              </FloatingPanel>
            </FloatingPanelContainer>,
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
  <BadgeShell data-minimal="Badge" data-minimal-tone={tone} data-minimal-emphasis={emphasis} $size={size} {...props}>
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
    <AlertShell data-minimal="Alert" data-minimal-tone={tone} role={liveRole} aria-live={liveMode} {...props}>
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
          data-minimal-selected={Boolean(selected)}
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
  variant = "neutral",
  ...props
}: MinimalSegmentedControlProps<T>) => {
  const currentIndex = options.findIndex((option) => option.value === value);

  return (
    <SegmentedShell data-minimal="SegmentedControl" $size={size} role="group" aria-label={ariaLabel} {...props}>
      {currentIndex >= 0 ? (
        <SegmentedIndicator
          $index={currentIndex}
          $count={options.length}
          $variant={variant}
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
            $variant={variant}
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
  const presence = useMinimalPresence(Boolean(isOpen), minimalMotionMs.standard + 20);

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
      {presence.mounted ? (
        <ExplainerPanel data-state={presence.state} aria-hidden={presence.state === "open" ? undefined : true}>
          <ExplainerPanelClip>
            <ExplainerPanelBody>{children}</ExplainerPanelBody>
          </ExplainerPanelClip>
        </ExplainerPanel>
      ) : null}
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
  <StatShell data-minimal="StatCard" data-minimal-tone={tone} {...props}>
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

export const MinimalStack = ({
  children,
  rhythm = "sm",
  ...props
}: MinimalStackProps) => (
  <StackShell data-minimal="Stack" $rhythm={rhythm} {...props}>
    {children}
  </StackShell>
);

/*
 * Offscreen culling for long pages (research doc P4).
 *
 * `content-visibility: auto` lets the browser skip style, layout and paint for
 * a section that is outside the viewport, the DOM equivalent of frustum
 * culling. The content stays in the document: it is still in the accessibility
 * tree and still found by find-in-page. Animations inside a skipped section are
 * not updated either, so an offscreen skeleton shimmer stops costing frames.
 *
 * `contain-intrinsic-size: auto <estimate>` is what makes skipping safe. A
 * skipped section needs a size or the scroll height collapses; `auto` makes the
 * browser remember the size it last rendered at, so the estimate only matters
 * for a section that has never been on screen. The estimate reaches CSS as a
 * variable on the element, which keeps the rule itself static.
 */
const CullSectionShell = styled.div`
  content-visibility: auto;
  contain-intrinsic-size: auto var(--minimal-cull-estimate, 600px);
`;

export const MinimalCullSection = ({
  children,
  estimatedSize = "600px",
  style,
  ...props
}: MinimalCullSectionProps) => (
  <CullSectionShell
    data-minimal="CullSection"
    style={{ "--minimal-cull-estimate": estimatedSize, ...style } as CSSProperties}
    {...props}
  >
    {children}
  </CullSectionShell>
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

const CalendarChevron = ({ direction }: { direction: "left" | "right" | "down" }) => {
  const points = direction === "left" ? "15 18 9 12 15 6" : direction === "right" ? "9 18 15 12 9 6" : "6 9 12 15 18 9";
  return (
    <svg aria-hidden width="14" height="14" viewBox="0 0 24 24" fill="none">
      <polyline points={points} stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
};

export const MinimalCalendar = ({
  value,
  onChange,
  month,
  onMonthChange,
  minDate,
  maxDate,
  isDateDisabled,
  weekStartsOn = 1,
  locale = "en-US",
  showAdjacentDays = true,
  showTodayAction = true,
  renderDayContent,
  ...props
}: MinimalCalendarProps) => {
  const selectedDate = normalizeDateValue(value);
  const selectedTimestamp = selectedDate?.getTime();
  const externalMonth = normalizeDateValue(month);
  const minimumDate = normalizeDateValue(minDate);
  const maximumDate = normalizeDateValue(maxDate);
  const today = normalizeDateValue(new Date()) as Date;
  const [viewMode, setViewMode] = useState<CalendarViewMode>("day");
  const [direction, setDirection] = useState(0);
  const [internalMonth, setInternalMonth] = useState(
    externalMonth ?? selectedDate ?? new Date(today.getFullYear(), today.getMonth(), 1)
  );
  const visibleMonth = externalMonth ?? internalMonth;
  const visibleYear = visibleMonth.getFullYear();
  const visibleMonthIndex = visibleMonth.getMonth();
  const yearPageStart = Math.floor(visibleYear / 16) * 16;
  const days = useMemo(
    () => buildMonthGrid(new Date(visibleYear, visibleMonthIndex, 1), weekStartsOn),
    [visibleYear, visibleMonthIndex, weekStartsOn]
  );
  const weekdayFormatter = useMemo(() => new Intl.DateTimeFormat(locale, { weekday: "short" }), [locale]);
  const monthFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { month: "long", year: "numeric" }),
    [locale]
  );
  const monthNameFormatter = useMemo(() => new Intl.DateTimeFormat(locale, { month: "short" }), [locale]);
  const dayFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { weekday: "long", year: "numeric", month: "long", day: "numeric" }),
    [locale]
  );
  const weekDays = Array.from({ length: 7 }, (_, index) => {
    const base = new Date(2024, 0, 7 + ((index + weekStartsOn) % 7));
    return weekdayFormatter.format(base);
  });
  const monthOptions = Array.from({ length: 12 }, (_, index) => ({
    index,
    label: monthNameFormatter.format(new Date(2024, index, 1)),
  }));
  const yearOptions = Array.from({ length: 16 }, (_, index) => yearPageStart + index);
  const [focusedDate, setFocusedDate] = useState<Date>(
    () => selectedDate ?? new Date(visibleYear, visibleMonthIndex, 1)
  );
  const gridRef = useRef<HTMLDivElement>(null);

  const updateMonth = (next: Date) => {
    const normalized = new Date(next.getFullYear(), next.getMonth(), 1);
    if (!externalMonth) setInternalMonth(normalized);
    onMonthChange?.(normalized);
  };

  const dateIsOutsideBounds = (date: Date) => {
    const timestamp = new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
    const minimum = minimumDate?.getTime();
    const maximum = maximumDate?.getTime();
    return (minimum !== undefined && timestamp < minimum) || (maximum !== undefined && timestamp > maximum);
  };

  const isDisabled = (date: Date) => dateIsOutsideBounds(date) || Boolean(isDateDisabled?.(date));

  const canNavigateToMonth = (candidate: Date) => {
    const first = new Date(candidate.getFullYear(), candidate.getMonth(), 1);
    const last = new Date(candidate.getFullYear(), candidate.getMonth() + 1, 0);
    return !(minimumDate && last < minimumDate) && !(maximumDate && first > maximumDate);
  };

  const canNavigateToYear = (year: number) => {
    const first = new Date(year, 0, 1);
    const last = new Date(year, 11, 31);
    return !(minimumDate && last < minimumDate) && !(maximumDate && first > maximumDate);
  };

  const canNavigateYearPage = (startYear: number) => {
    const first = new Date(startYear, 0, 1);
    const last = new Date(startYear + 15, 11, 31);
    return !(minimumDate && last < minimumDate) && !(maximumDate && first > maximumDate);
  };

  const periodCandidate = (delta: number) => {
    if (viewMode === "day") return addMonths(visibleMonth, delta);
    if (viewMode === "month") return new Date(visibleYear + delta, visibleMonthIndex, 1);
    return new Date(visibleYear + delta * 16, visibleMonthIndex, 1);
  };

  const canNavigatePeriod = (delta: number) => {
    const candidate = periodCandidate(delta);
    if (viewMode === "day") return canNavigateToMonth(candidate);
    if (viewMode === "month") return canNavigateToYear(candidate.getFullYear());
    return canNavigateYearPage(yearPageStart + delta * 16);
  };

  const navigatePeriod = (delta: number) => {
    if (!canNavigatePeriod(delta)) return;
    setDirection(delta);
    updateMonth(periodCandidate(delta));
  };

  const adjustMonthForFocusedDate = (next: Date) => {
    setFocusedDate(next);
    if (next.getMonth() !== visibleMonthIndex || next.getFullYear() !== visibleYear) {
      setDirection(next > visibleMonth ? 1 : -1);
      updateMonth(next);
    }
  };

  useEffect(() => {
    if (selectedTimestamp !== undefined) setFocusedDate(new Date(selectedTimestamp));
  }, [selectedTimestamp]);

  useEffect(() => {
    if (focusedDate.getMonth() !== visibleMonthIndex || focusedDate.getFullYear() !== visibleYear) {
      setFocusedDate(new Date(visibleYear, visibleMonthIndex, 1));
    }
  }, [focusedDate, visibleMonthIndex, visibleYear]);

  useEffect(() => {
    if (!gridRef.current || !gridRef.current.contains(document.activeElement)) return;
    gridRef.current
      .querySelector<HTMLButtonElement>(`[data-date="${focusedDate.toISOString()}"]`)
      ?.focus();
  }, [focusedDate]);

  const handleDayKeyDown = (day: Date, event: React.KeyboardEvent<HTMLButtonElement>) => {
    let next: Date | null = null;
    if (event.key === "ArrowLeft") next = new Date(day.getFullYear(), day.getMonth(), day.getDate() - 1);
    if (event.key === "ArrowRight") next = new Date(day.getFullYear(), day.getMonth(), day.getDate() + 1);
    if (event.key === "ArrowUp") next = new Date(day.getFullYear(), day.getMonth(), day.getDate() - 7);
    if (event.key === "ArrowDown") next = new Date(day.getFullYear(), day.getMonth(), day.getDate() + 7);
    if (event.key === "PageUp") next = new Date(day.getFullYear(), day.getMonth() - 1, day.getDate());
    if (event.key === "PageDown") next = new Date(day.getFullYear(), day.getMonth() + 1, day.getDate());
    if (event.key === "Home") next = startOfWeek(day, weekStartsOn);
    if (event.key === "End") {
      const start = startOfWeek(day, weekStartsOn);
      next = new Date(start.getFullYear(), start.getMonth(), start.getDate() + 6);
    }
    if (!next) return;
    event.preventDefault();
    if (!dateIsOutsideBounds(next)) adjustMonthForFocusedDate(next);
  };

  const selectYear = (year: number) => {
    const preferred = new Date(year, visibleMonthIndex, 1);
    const candidate = canNavigateToMonth(preferred)
      ? preferred
      : monthOptions.map(({ index }) => new Date(year, index, 1)).find(canNavigateToMonth);
    if (!candidate) return;
    setDirection(year >= visibleYear ? 1 : -1);
    updateMonth(candidate);
    setViewMode("month");
  };

  const title = viewMode === "day"
    ? monthFormatter.format(visibleMonth)
    : viewMode === "month"
      ? String(visibleYear)
      : `${yearPageStart}–${yearPageStart + 15}`;
  const periodName = viewMode === "day" ? "month" : viewMode === "month" ? "year" : "year range";
  const viewKey = `${viewMode}:${viewMode === "year" ? yearPageStart : `${visibleYear}:${visibleMonthIndex}`}`;

  return (
    <CalendarShell data-minimal="Calendar" aria-label="Calendar" {...props}>
      <CalendarHeader>
        <CalendarTitleButton
          type="button"
          disabled={viewMode === "year"}
          aria-label={viewMode === "day" ? "Choose month" : viewMode === "month" ? "Choose year" : title}
          onClick={() => {
            setDirection(0);
            setViewMode(viewMode === "day" ? "month" : "year");
          }}
        >
          <span aria-live="polite">{title}</span>
          {viewMode !== "year" ? <CalendarChevron direction="down" /> : null}
        </CalendarTitleButton>
        <CalendarNavGroup>
          <CalendarNavButton
            type="button"
            aria-label={`Previous ${periodName}`}
            disabled={!canNavigatePeriod(-1)}
            onClick={() => navigatePeriod(-1)}
          >
            <CalendarChevron direction="left" />
          </CalendarNavButton>
          <CalendarNavButton
            type="button"
            aria-label={`Next ${periodName}`}
            disabled={!canNavigatePeriod(1)}
            onClick={() => navigatePeriod(1)}
          >
            <CalendarChevron direction="right" />
          </CalendarNavButton>
        </CalendarNavGroup>
      </CalendarHeader>

      <CalendarViewport>
          <CalendarViewPanel
            key={viewKey}
            style={{ "--minimal-motion-shift": direction === 0 ? "0px" : direction > 0 ? "18px" : "-18px" } as CSSProperties}
          >
            {viewMode === "day" ? (
              <CalendarGrid ref={gridRef}>
                {weekDays.map((day, index) => (
                  <CalendarWeekday key={`${day}:${index}`}>{day}</CalendarWeekday>
                ))}
                {days.map((day) => {
                  const selected = sameDay(selectedDate, day);
                  const currentMonth = day.getMonth() === visibleMonthIndex;
                  if (!showAdjacentDays && !currentMonth) {
                    return <CalendarBlank key={day.toISOString()} aria-hidden />;
                  }
                  const focused = sameDay(focusedDate, day);
                  const disabled = isDisabled(day);
                  const isToday = sameDay(today, day);
                  return (
                    <CalendarDay
                      key={day.toISOString()}
                      data-date={day.toISOString()}
                      type="button"
                      data-minimal-selected={Boolean(selected)}
                      data-minimal-current-month={Boolean(currentMonth)}
                      data-minimal-disabled={Boolean(disabled)}
                      data-minimal-today={Boolean(isToday)}
                      $selected={selected}
                      $hasContent={Boolean(renderDayContent)}
                      aria-label={dayFormatter.format(day)}
                      aria-current={isToday ? "date" : undefined}
                      aria-selected={selected}
                      aria-disabled={disabled || undefined}
                      tabIndex={focused ? 0 : -1}
                      onKeyDown={(event) => handleDayKeyDown(day, event)}
                      onClick={() => {
                        setFocusedDate(day);
                        if (!disabled) onChange?.(day);
                      }}
                    >
                      {selected ? (
                        <CalendarSelection />
                      ) : null}
                      <CalendarDayContent>
                        <span>{day.getDate()}</span>
                        {renderDayContent?.(day, selected, currentMonth)}
                      </CalendarDayContent>
                    </CalendarDay>
                  );
                })}
              </CalendarGrid>
            ) : viewMode === "month" ? (
              <CalendarSelectorGrid $columns={3} role="group" aria-label={`Months in ${visibleYear}`}>
                {monthOptions.map(({ index, label }) => {
                  const candidate = new Date(visibleYear, index, 1);
                  return (
                    <CalendarOption
                      key={index}
                      type="button"
                      $active={index === visibleMonthIndex}
                      disabled={!canNavigateToMonth(candidate)}
                      aria-label={new Intl.DateTimeFormat(locale, { month: "long", year: "numeric" }).format(candidate)}
                      onClick={() => {
                        setDirection(index >= visibleMonthIndex ? 1 : -1);
                        updateMonth(candidate);
                        setViewMode("day");
                      }}
                    >
                      {label}
                    </CalendarOption>
                  );
                })}
              </CalendarSelectorGrid>
            ) : (
              <CalendarSelectorGrid $columns={4} role="group" aria-label={`Years ${yearPageStart} to ${yearPageStart + 15}`}>
                {yearOptions.map((year) => (
                  <CalendarOption
                    key={year}
                    type="button"
                    $active={year === visibleYear}
                    disabled={!canNavigateToYear(year)}
                    onClick={() => selectYear(year)}
                  >
                    {year}
                  </CalendarOption>
                ))}
              </CalendarSelectorGrid>
            )}
          </CalendarViewPanel>
      </CalendarViewport>

      {showTodayAction ? (
        <CalendarFooter>
          <CalendarTodayButton
            type="button"
            disabled={dateIsOutsideBounds(today)}
            onClick={() => {
              setDirection(today >= visibleMonth ? 1 : -1);
              setViewMode("day");
              setFocusedDate(today);
              updateMonth(today);
            }}
          >
            Today
          </CalendarTodayButton>
        </CalendarFooter>
      ) : null}
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
  const dismissRefs = useMemo(() => [modalRef], []);

  useDismissLayer(dismissRefs, open, onClose);
  useFocusTrap(modalRef, open);

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
    <>
      <ModalBackdrop onClick={onClose} />
      <ModalShell
        data-minimal="ActionModal"
        ref={modalRef}
        data-minimal-mobile-sheet={mobileSheet}
        $mobileSheet={mobileSheet}
        role="dialog"
        aria-modal="true"
        tabIndex={-1}
        style={{
          "--minimal-modal-max-width": maxWidth,
          "--minimal-modal-max-height": maxHeight,
        } as CSSProperties}
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
    </>,
    document.body
  );
};
