import { Transition, Variants, useReducedMotion } from "framer-motion";

import { minimalBaseTheme, useMinimalTheme } from "./theme";
import type { MinimalTheme } from "./types";

const noMotionTransition: Transition = { duration: 0 };

export const createMicroTransition = (reducedMotion: boolean, theme: MinimalTheme = minimalBaseTheme): Transition =>
  reducedMotion
    ? noMotionTransition
    : {
        duration: theme.motion.microDuration,
        ease: theme.motion.standardEase,
      };

export const createStandardTransition = (reducedMotion: boolean, theme: MinimalTheme = minimalBaseTheme): Transition =>
  reducedMotion
    ? noMotionTransition
    : {
        duration: theme.motion.standardDuration,
        ease: theme.motion.standardEase,
      };

export const createSpringTransition = (reducedMotion: boolean, theme: MinimalTheme = minimalBaseTheme): Transition =>
  reducedMotion
    ? noMotionTransition
    : {
        type: "spring",
        stiffness: theme.motion.springStiffness,
        damping: theme.motion.springDamping,
      };

export const createFadeVariants = (reducedMotion: boolean, theme: MinimalTheme = minimalBaseTheme): Variants => ({
  initial: reducedMotion ? { opacity: 1 } : { opacity: 0 },
  animate: { opacity: 1, transition: createStandardTransition(reducedMotion, theme) },
  exit: reducedMotion ? { opacity: 1 } : { opacity: 0, transition: createStandardTransition(reducedMotion, theme) },
});

export const createPopVariants = (reducedMotion: boolean, theme: MinimalTheme = minimalBaseTheme): Variants => ({
  initial: reducedMotion ? { opacity: 1, scale: 1 } : { opacity: 0, scale: 0.97 },
  animate: { opacity: 1, scale: 1, transition: createStandardTransition(reducedMotion, theme) },
  exit: reducedMotion ? { opacity: 1, scale: 1 } : { opacity: 0, scale: 0.97, transition: createStandardTransition(reducedMotion, theme) },
});

export const createSlideUpVariants = (reducedMotion: boolean, theme: MinimalTheme = minimalBaseTheme): Variants => ({
  initial: reducedMotion ? { opacity: 1, y: 0 } : { opacity: 0, y: theme.motion.pageOffset },
  animate: { opacity: 1, y: 0, transition: createStandardTransition(reducedMotion, theme) },
  exit: reducedMotion
    ? { opacity: 1, y: 0 }
    : { opacity: 0, y: -theme.motion.pageOffset, transition: createStandardTransition(reducedMotion, theme) },
});

export const createTooltipVariants = (reducedMotion: boolean, theme: MinimalTheme = minimalBaseTheme): Variants => ({
  initial: reducedMotion ? { opacity: 1, scale: 1 } : { opacity: 0, scale: 0.96, y: 4 },
  animate: { opacity: 1, scale: 1, y: 0, transition: createMicroTransition(reducedMotion, theme) },
  exit: reducedMotion ? { opacity: 1, scale: 1 } : { opacity: 0, scale: 0.96, y: 4, transition: createMicroTransition(reducedMotion, theme) },
});

export const createPageTransitionVariants = (reducedMotion: boolean, theme: MinimalTheme = minimalBaseTheme): Variants => ({
  initial: reducedMotion ? { opacity: 1, y: 0 } : { opacity: 0, y: theme.motion.pageOffset },
  animate: {
    opacity: 1,
    y: 0,
    transition: reducedMotion
      ? noMotionTransition
      : { duration: theme.motion.slowDuration, ease: theme.motion.entranceEase },
  },
  exit: reducedMotion
    ? { opacity: 1, y: 0 }
    : {
        opacity: 0,
        y: -theme.motion.pageOffset,
        transition: { duration: theme.motion.standardDuration, ease: theme.motion.exitEase },
      },
});

export const useMinimalMotion = () => {
  const theme = useMinimalTheme();
  const reducedMotion = Boolean(useReducedMotion());

  return {
    reducedMotion,
    micro: createMicroTransition(reducedMotion, theme),
    standard: createStandardTransition(reducedMotion, theme),
    spring: createSpringTransition(reducedMotion, theme),
    fadeVariants: createFadeVariants(reducedMotion, theme),
    popVariants: createPopVariants(reducedMotion, theme),
    slideUpVariants: createSlideUpVariants(reducedMotion, theme),
    tooltipVariants: createTooltipVariants(reducedMotion, theme),
    pageVariants: createPageTransitionVariants(reducedMotion, theme),
  };
};
