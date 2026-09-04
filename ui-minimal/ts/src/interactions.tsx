import { type HTMLAttributes, type ReactNode, useEffect, useId, useMemo, useRef, useState } from "react";
import { css, styled } from "styled-components";

import { MinimalCalendar, MinimalDropdown, type MinimalOption } from "./primitives";
import type { MinimalSize } from "./types";

type FieldCopy = {
  label: ReactNode;
  description?: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
};

export interface MinimalCheckboxProps
  extends FieldCopy,
    Omit<HTMLAttributes<HTMLDivElement>, "defaultChecked" | "onChange"> {
  checked?: boolean;
  defaultChecked?: boolean;
  onCheckedChange?: (checked: boolean) => void;
  indeterminate?: boolean;
  disabled?: boolean;
  readOnly?: boolean;
  required?: boolean;
  name?: string;
  value?: string;
}

export interface MinimalSwitchProps
  extends FieldCopy,
    Omit<HTMLAttributes<HTMLDivElement>, "defaultChecked" | "onChange"> {
  checked?: boolean;
  defaultChecked?: boolean;
  onCheckedChange?: (checked: boolean) => void;
  disabled?: boolean;
  readOnly?: boolean;
  required?: boolean;
  name?: string;
  value?: string;
}

export interface MinimalNumberFieldProps extends FieldCopy {
  value?: number | null;
  defaultValue?: number;
  onValueChange?: (value: number | null) => void;
  onValueCommitted?: (value: number | null) => void;
  min?: number;
  max?: number;
  step?: number | "any";
  smallStep?: number;
  largeStep?: number;
  format?: Intl.NumberFormatOptions;
  locale?: Intl.LocalesArgument;
  name?: string;
  required?: boolean;
  disabled?: boolean;
  readOnly?: boolean;
  allowWheelScrub?: boolean;
  inputSize?: MinimalSize;
  decrementLabel?: string;
  incrementLabel?: string;
  placeholder?: string;
}

export interface MinimalTabOption<T extends string> {
  value: T;
  label: ReactNode;
  content: ReactNode;
  disabled?: boolean;
}

export interface MinimalTabsProps<T extends string>
  extends Omit<HTMLAttributes<HTMLDivElement>, "defaultValue" | "onChange"> {
  tabs: readonly MinimalTabOption<T>[];
  value?: T;
  defaultValue?: T;
  onValueChange?: (value: T) => void;
  ariaLabel: string;
  orientation?: "horizontal" | "vertical";
}

export interface MinimalDatePickerProps
  extends FieldCopy,
    Omit<HTMLAttributes<HTMLDivElement>, "defaultValue" | "onChange"> {
  value?: Date | string | null;
  onChange: (value: Date | null) => void;
  minDate?: Date | string | null;
  maxDate?: Date | string | null;
  isDateDisabled?: (date: Date) => boolean;
  locale?: string;
  weekStartsOn?: 0 | 1;
  placeholder?: string;
  disabled?: boolean;
  clearable?: boolean;
  dateFormat?: Intl.DateTimeFormatOptions;
}

export interface MinimalTimePickerProps
  extends Omit<HTMLAttributes<HTMLDivElement>, "defaultValue" | "onChange"> {
  value?: string;
  onChange: (value: string) => void;
  label?: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  minTime?: string;
  maxTime?: string;
  intervalMinutes?: number;
  locale?: string;
  hour12?: boolean;
  disabled?: boolean;
  placeholder?: ReactNode;
}

const focusRing = css`
  &:focus-visible {
    outline: 2px solid ${({ theme }) => theme.color.borderFocus};
    outline-offset: 2px;
  }
`;

const controlReset = css`
  appearance: none;
  border: 0;
  background: transparent;
  color: inherit;
  font: inherit;
`;

const popIn = css`
  @media (prefers-reduced-motion: no-preference) {
    animation: minimal-interactions-pop 140ms cubic-bezier(0.22, 1, 0.36, 1);
  }

  @keyframes minimal-interactions-pop {
    from {
      opacity: 0;
      transform: scale(0.98);
    }
    to {
      opacity: 1;
      transform: scale(1);
    }
  }
`;

const Style = {
  Field: styled.div`
    display: grid;
    gap: ${({ theme }) => theme.space["2xs"]};
    width: 100%;
  `,
  Label: styled.label`
    color: ${({ theme }) => theme.color.textPrimary};
    font-size: ${({ theme }) => theme.typography.captionSize};
    font-weight: ${({ theme }) => theme.typography.weightSemibold};
  `,
  Description: styled.div`
    color: ${({ theme }) => theme.color.textTertiary};
    font-size: ${({ theme }) => theme.typography.captionSize};
    line-height: ${({ theme }) => theme.typography.lineHeightBody};
  `,
  Message: styled.p<{ $error: boolean }>`
    margin: 0;
    color: ${({ theme, $error }) => ($error ? theme.color.danger : theme.color.textSecondary)};
    font-size: ${({ theme }) => theme.typography.captionSize};
    line-height: ${({ theme }) => theme.typography.lineHeightBody};
  `,
  ChoiceLabel: styled.label<{ $disabled: boolean }>`
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: ${({ theme }) => theme.space.xs};
    align-items: start;
    cursor: ${({ $disabled }) => ($disabled ? "not-allowed" : "pointer")};
    opacity: ${({ $disabled }) => ($disabled ? 0.56 : 1)};
  `,
  ChoiceCopy: styled.span`
    display: grid;
    gap: ${({ theme }) => theme.space["3xs"]};
    min-width: 0;
    padding-block: 4px;
  `,
  ChoiceTitle: styled.span`
    color: ${({ theme }) => theme.color.textPrimary};
    font-size: ${({ theme }) => theme.typography.bodySize};
    font-weight: ${({ theme }) => theme.typography.weightMedium};
  `,
  ChoiceControl: styled.span`
    position: relative;
    display: inline-grid;
    place-items: center;
    width: var(--minimal-control-min-target);
    height: var(--minimal-control-min-target);
    margin-block: 4px;

    & > input:focus-visible + span {
      outline: 2px solid ${({ theme }) => theme.color.borderFocus};
      outline-offset: 2px;
    }

    & > input:checked + span,
    & > input:indeterminate + span {
      border-color: ${({ theme }) => theme.color.brand};
      background: ${({ theme }) => theme.color.brand};
      color: ${({ theme }) => theme.color.textInverse};

      & > span {
        opacity: 1;
      }
    }
  `,
  ControlInput: styled.input`
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    margin: 0;
    opacity: 0;
    cursor: inherit;
  `,
  CheckboxBox: styled.span`
    display: inline-grid;
    place-items: center;
    width: var(--minimal-control-min-target);
    height: var(--minimal-control-min-target);
    border: 1px solid ${({ theme }) => theme.color.borderStrong};
    border-radius: ${({ theme }) => theme.radius.sm};
    background: ${({ theme }) => theme.color.bgSurface};

    & > span {
      font-size: 1rem;
      font-weight: ${({ theme }) => theme.typography.weightBold};
      line-height: 1;
      opacity: 0;
    }
  `,
  SwitchTrack: styled.span`
    position: relative;
    display: inline-flex;
    align-items: center;
    flex: 0 0 auto;
    width: 46px;
    height: 28px;
    padding: 0 3px;
    margin-block: 8px;
    border: 1px solid ${({ theme }) => theme.color.borderStrong};
    border-radius: ${({ theme }) => theme.radius.pill};
    background: ${({ theme }) => theme.color.bgSurfaceAlt};
    transition:
      background-color 160ms cubic-bezier(0.22, 1, 0.36, 1),
      border-color 160ms cubic-bezier(0.22, 1, 0.36, 1);

    & > input:focus-visible + span {
      outline: 2px solid ${({ theme }) => theme.color.borderFocus};
      outline-offset: 2px;
    }

    &:has(input:checked) {
      border-color: ${({ theme }) => theme.color.brand};
      background: ${({ theme }) => theme.color.brand};
    }

    @media (prefers-reduced-motion: reduce) {
      transition: none;

      & > span {
        transition: none;
      }
    }
  `,
  SwitchThumb: styled.span`
    display: block;
    width: 20px;
    height: 20px;
    border-radius: 50%;
    background: ${({ theme }) => theme.color.bgSurface};
    border: 1px solid ${({ theme }) => theme.color.borderSubtle};
    box-shadow: 0 1px 3px rgba(28, 28, 30, 0.24);
    transition: transform 160ms cubic-bezier(0.22, 1, 0.36, 1);

    input:checked + & {
      border-color: transparent;
      transform: translateX(18px);
    }
  `,
  NumberRoot: styled.div`
    display: grid;
    gap: ${({ theme }) => theme.space["2xs"]};
  `,
  NumberGroup: styled.div<{ $size: MinimalSize; $invalid: boolean }>`
    display: grid;
    grid-template-columns: var(--minimal-control-min-target) minmax(0, 1fr) var(--minimal-control-min-target);
    min-height: ${({ $size }) => `var(--minimal-control-height-${$size})`};
    overflow: hidden;
    border: 1px solid ${({ theme, $invalid }) => ($invalid ? theme.color.danger : theme.color.borderSubtle)};
    border-radius: ${({ theme }) => theme.radius.sm};
    background: ${({ theme }) => theme.color.bgSurface};

    &:focus-within {
      border-color: ${({ theme, $invalid }) => ($invalid ? theme.color.danger : theme.color.borderFocus)};
      /* Built in one interpolation rather than two. A declaration whose value
         wraps onto a following line that begins with an interpolation is
         unparseable to the CSS-in-JS language service, which then reports a
         spurious "semi-colon expected" for the whole block. */
      box-shadow: ${({ theme, $invalid }) =>
        `0 0 0 ${theme.focus.ringWidth} ${$invalid ? theme.color.dangerSoft : theme.color.brandSoft}`};
    }
  `,
  NumberButton: styled.button`
    ${controlReset}
    ${focusRing}
    min-width: var(--minimal-control-min-target);
    cursor: pointer;
    color: ${({ theme }) => theme.color.textSecondary};
    background: ${({ theme }) => theme.color.bgSurfaceAlt};

    &:disabled {
      cursor: not-allowed;
      opacity: 0.48;
    }
  `,
  NumberInput: styled.input`
    ${controlReset}
    min-width: 0;
    width: 100%;
    outline: 0;
    color: ${({ theme }) => theme.color.textPrimary};
    padding: 0 ${({ theme }) => theme.space.xs};
    text-align: center;
  `,
  TabsRoot: styled.div`
    display: grid;
    gap: ${({ theme }) => theme.space.sm};
  `,
  TabsList: styled.div`
    display: flex;
    gap: ${({ theme }) => theme.space["2xs"]};
    overflow-x: auto;
    padding: ${({ theme }) => theme.space["2xs"]};
    border: 1px solid ${({ theme }) => theme.color.borderSubtle};
    border-radius: ${({ theme }) => theme.radius.md};
    background: ${({ theme }) => theme.color.bgSurfaceAlt};

    &[data-orientation="vertical"] {
      flex-direction: column;
    }
  `,
  Tab: styled.button`
    ${controlReset}
    ${focusRing}
    min-height: var(--minimal-control-min-target);
    padding: 8px 14px;
    border-radius: ${({ theme }) => theme.radius.sm};
    color: ${({ theme }) => theme.color.textSecondary};
    cursor: pointer;
    white-space: nowrap;

    &[aria-selected="true"] {
      background: ${({ theme }) => theme.color.bgSurface};
      color: ${({ theme }) => theme.color.textPrimary};
      box-shadow: ${({ theme }) => theme.shadow.subtle};
    }

    &:disabled {
      cursor: not-allowed;
      opacity: 0.48;
    }
  `,
  TabPanel: styled.div`
    min-width: 0;
    outline: none;
  `,
  Anchor: styled.span`
    position: relative;
    display: grid;
    width: 100%;
  `,
  DateTrigger: styled.button<{ $placeholder: boolean; $invalid: boolean }>`
    ${controlReset}
    ${focusRing}
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: ${({ theme }) => theme.space.xs};
    width: 100%;
    min-height: var(--minimal-control-height-md);
    padding: 10px 14px;
    border: 1px solid ${({ theme, $invalid }) => ($invalid ? theme.color.danger : theme.color.borderSubtle)};
    border-radius: ${({ theme }) => theme.radius.sm};
    background: ${({ theme }) => theme.color.bgSurface};
    color: ${({ theme, $placeholder }) => ($placeholder ? theme.color.textTertiary : theme.color.textPrimary)};
    cursor: pointer;
    text-align: left;

    &:disabled {
      cursor: not-allowed;
      opacity: 0.56;
    }
  `,
  DateTriggerValue: styled.span`
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-weight: ${({ theme }) => theme.typography.weightMedium};
  `,
  DateTriggerIcon: styled.span`
    display: inline-flex;
    flex: 0 0 auto;
    color: ${({ theme }) => theme.color.textSecondary};
  `,
  DatePanel: styled.div<{ $open: boolean }>`
    position: absolute;
    top: calc(100% + var(--minimal-overlay-anchored-offset));
    left: 0;
    z-index: ${({ theme }) => theme.zIndex.dropdown};
    width: min(calc(100vw - (2 * var(--minimal-overlay-viewport-gutter))), 368px);
    max-height: var(--minimal-overlay-max-height);
    overflow: auto;
    visibility: ${({ $open }) => ($open ? "visible" : "hidden")};
    border: 1px solid ${({ theme }) => theme.color.borderStrong};
    border-radius: ${({ theme }) => theme.radius.md};
    background: ${({ theme }) => theme.color.bgSurface};
    box-shadow: ${({ theme }) => theme.shadow.floating};

    ${({ $open }) => ($open ? popIn : "")}

    & [data-minimal="Calendar"] {
      border: 0;
      border-radius: inherit;
    }
  `,
  VisuallyHidden: styled.span`
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  `,
  ClearButton: styled.button`
    ${controlReset}
    ${focusRing}
    justify-self: start;
    min-height: var(--minimal-control-min-target);
    color: ${({ theme }) => theme.color.textSecondary};
    cursor: pointer;
  `,
};

const ChoiceMessage = ({ description, hint, error }: Pick<FieldCopy, "description" | "hint" | "error">) => (
  <>
    {description ? <Style.Description>{description}</Style.Description> : null}
    {error ? (
      <Style.Message $error role="alert">{error}</Style.Message>
    ) : hint ? (
      <Style.Message $error={false}>{hint}</Style.Message>
    ) : null}
  </>
);

export const MinimalCheckbox = ({
  label,
  description,
  hint,
  error,
  checked,
  defaultChecked,
  onCheckedChange,
  indeterminate,
  disabled = false,
  readOnly,
  required,
  name,
  value,
  ...props
}: MinimalCheckboxProps) => {
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (inputRef.current) {
      inputRef.current.indeterminate = Boolean(indeterminate);
    }
  }, [indeterminate]);

  return (
    <div data-minimal="Checkbox" {...props}>
      <Style.ChoiceLabel $disabled={disabled}>
        <Style.ChoiceControl data-indeterminate={indeterminate || undefined}>
          <Style.ControlInput
            ref={inputRef}
            type="checkbox"
            checked={checked}
            defaultChecked={defaultChecked}
            onChange={(event) => onCheckedChange?.(event.currentTarget.checked)}
            disabled={disabled}
            readOnly={readOnly}
            required={required}
            name={name}
            value={value}
          />
          <Style.CheckboxBox aria-hidden>
            <span>{indeterminate ? "−" : "✓"}</span>
          </Style.CheckboxBox>
        </Style.ChoiceControl>
        <Style.ChoiceCopy>
          <Style.ChoiceTitle>{label}</Style.ChoiceTitle>
          <ChoiceMessage description={description} hint={hint} error={error} />
        </Style.ChoiceCopy>
      </Style.ChoiceLabel>
    </div>
  );
};

export const MinimalSwitch = ({
  label,
  description,
  hint,
  error,
  checked,
  defaultChecked,
  onCheckedChange,
  disabled = false,
  readOnly,
  required,
  name,
  value,
  ...props
}: MinimalSwitchProps) => (
  <div data-minimal="Switch" {...props}>
    <Style.ChoiceLabel $disabled={disabled}>
      <Style.SwitchTrack>
        <Style.ControlInput
          type="checkbox"
          role="switch"
          checked={checked}
          defaultChecked={defaultChecked}
          onChange={(event) => onCheckedChange?.(event.currentTarget.checked)}
          disabled={disabled}
          readOnly={readOnly}
          required={required}
          name={name}
          value={value}
        />
        <Style.SwitchThumb />
      </Style.SwitchTrack>
      <Style.ChoiceCopy>
        <Style.ChoiceTitle>{label}</Style.ChoiceTitle>
        <ChoiceMessage description={description} hint={hint} error={error} />
      </Style.ChoiceCopy>
    </Style.ChoiceLabel>
  </div>
);

const numericStep = (step: MinimalNumberFieldProps["step"], multiplier: number) =>
  step === "any" ? multiplier : (step ?? 1) * multiplier;

const clampNumber = (candidate: number, min?: number, max?: number) => {
  let next = candidate;
  if (min !== undefined && Number.isFinite(min)) {
    next = Math.max(min, next);
  }
  if (max !== undefined && Number.isFinite(max)) {
    next = Math.min(max, next);
  }
  return next;
};

export const MinimalNumberField = ({
  label,
  description,
  hint,
  error,
  value,
  defaultValue,
  onValueChange,
  onValueCommitted,
  min,
  max,
  step,
  smallStep,
  largeStep,
  name,
  required,
  disabled,
  readOnly,
  allowWheelScrub,
  inputSize = "md",
  decrementLabel = "Decrease value",
  incrementLabel = "Increase value",
  placeholder,
}: MinimalNumberFieldProps) => {
  const generatedId = useId();
  const id = `minimal-number-${generatedId}`;
  const hintId = hint ? `${id}-hint` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  const controlled = value !== undefined;
  const [draft, setDraft] = useState(() => String(value ?? defaultValue ?? ""));
  const current = controlled ? value : draft === "" ? null : Number(draft);

  const apply = (next: number | null, committed: boolean) => {
    if (!controlled) {
      setDraft(next === null ? "" : String(next));
    }
    onValueChange?.(next);
    if (committed) {
      onValueCommitted?.(next);
    }
  };

  const stepBy = (direction: 1 | -1, event: { shiftKey?: boolean; altKey?: boolean }) => {
    const magnitude = numericStep(step, event.shiftKey ? (largeStep ?? 10) : event.altKey ? (smallStep ?? 0.1) : 1);
    const base = typeof current === "number" && Number.isFinite(current) ? current : (defaultValue ?? 0);
    if (step === "any" || !Number.isInteger(magnitude)) {
      apply(clampNumber(Number((base + direction * magnitude).toFixed(6)), min, max), true);
      return;
    }
    apply(clampNumber(base + direction * magnitude, min, max), true);
  };

  return (
    <Style.NumberRoot data-minimal="NumberField">
      <Style.Label htmlFor={id}>{label}</Style.Label>
      {description ? <Style.Description>{description}</Style.Description> : null}
      <Style.NumberGroup $size={inputSize} $invalid={Boolean(error)}>
        <Style.NumberButton
          type="button"
          aria-label={decrementLabel}
          disabled={disabled || readOnly}
          onClick={(event) => stepBy(-1, event)}
        >
          −
        </Style.NumberButton>
        <Style.NumberInput
          id={id}
          name={name}
          required={required}
          disabled={disabled}
          readOnly={readOnly}
          role="spinbutton"
          inputMode="decimal"
          autoComplete="off"
          placeholder={placeholder}
          aria-valuenow={typeof current === "number" ? current : undefined}
          aria-valuemin={min}
          aria-valuemax={max}
          aria-invalid={Boolean(error) || undefined}
          aria-describedby={[hintId, errorId].filter(Boolean).join(" ") || undefined}
          value={draft}
          onChange={(event) => {
            if (controlled) {
              setDraft(event.currentTarget.value);
            } else {
              setDraft(event.currentTarget.value);
              const parsed = event.currentTarget.value.trim() === "" ? null : Number(event.currentTarget.value);
              onValueChange?.(parsed !== null && Number.isNaN(parsed) ? null : parsed);
            }
          }}
          onBlur={() => {
            const trimmed = draft.trim();
            if (trimmed === "") {
              apply(null, true);
              return;
            }
            const parsed = Number(trimmed);
            if (Number.isNaN(parsed)) {
              setDraft(typeof current === "number" ? String(current) : "");
              return;
            }
            apply(clampNumber(parsed, min, max), true);
          }}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.currentTarget.blur();
              return;
            }
            if (event.key === "ArrowUp") {
              event.preventDefault();
              stepBy(1, event);
            }
            if (event.key === "ArrowDown") {
              event.preventDefault();
              stepBy(-1, event);
            }
          }}
          onWheel={
            allowWheelScrub && !disabled && !readOnly
              ? (event) => {
                  event.preventDefault();
                  stepBy(event.deltaY < 0 ? 1 : -1, event);
                }
              : undefined
          }
        />
        <Style.NumberButton
          type="button"
          aria-label={incrementLabel}
          disabled={disabled || readOnly}
          onClick={(event) => stepBy(1, event)}
        >
          +
        </Style.NumberButton>
      </Style.NumberGroup>
      {error ? (
        <Style.Message id={errorId} $error role="alert">{error}</Style.Message>
      ) : hint ? (
        <Style.Message id={hintId} $error={false}>{hint}</Style.Message>
      ) : null}
    </Style.NumberRoot>
  );
};

export const MinimalTabs = <T extends string>({
  tabs,
  value,
  defaultValue,
  onValueChange,
  ariaLabel,
  orientation = "horizontal",
  ...props
}: MinimalTabsProps<T>) => {
  const generatedId = useId();
  const [innerValue, setInnerValue] = useState<T | undefined>(defaultValue);
  const selected = (value !== undefined ? value : innerValue) ?? tabs[0]?.value;
  const tabRefs = useRef(new Map<T, HTMLButtonElement>());

  const select = (next: T) => {
    if (value === undefined) {
      setInnerValue(next);
    }
    onValueChange?.(next);
    tabRefs.current.get(next)?.focus();
  };

  const moveSelection = (from: T, offset: 1 | -1) => {
    const enabled = tabs.filter((tab) => !tab.disabled);
    if (enabled.length === 0) {
      return;
    }
    const at = enabled.findIndex((tab) => tab.value === from);
    const next = enabled[(((at === -1 ? 0 : at) + offset) % enabled.length + enabled.length) % enabled.length];
    select(next.value);
  };

  return (
    <Style.TabsRoot data-minimal="Tabs" {...props}>
      <Style.TabsList role="tablist" aria-label={ariaLabel} data-orientation={orientation}>
        {tabs.map((tab) => (
          <Style.Tab
            key={tab.value}
            type="button"
            role="tab"
            id={`minimal-tab-${generatedId}-${tab.value}`}
            aria-selected={tab.value === selected}
            aria-controls={`minimal-tabpanel-${generatedId}-${tab.value}`}
            tabIndex={tab.value === selected ? 0 : -1}
            disabled={tab.disabled}
            ref={(node) => {
              if (node) {
                tabRefs.current.set(tab.value, node);
              } else {
                tabRefs.current.delete(tab.value);
              }
            }}
            onClick={() => select(tab.value)}
            onKeyDown={(event) => {
              const forward = orientation === "vertical" ? "ArrowDown" : "ArrowRight";
              const backward = orientation === "vertical" ? "ArrowUp" : "ArrowLeft";
              if (event.key === forward) {
                event.preventDefault();
                moveSelection(tab.value, 1);
              }
              if (event.key === backward) {
                event.preventDefault();
                moveSelection(tab.value, -1);
              }
              if (event.key === "Home") {
                event.preventDefault();
                const first = tabs.find((candidate) => !candidate.disabled);
                if (first) {
                  select(first.value);
                }
              }
              if (event.key === "End") {
                event.preventDefault();
                const last = [...tabs].reverse().find((candidate) => !candidate.disabled);
                if (last) {
                  select(last.value);
                }
              }
            }}
          >
            {tab.label}
          </Style.Tab>
        ))}
      </Style.TabsList>
      {tabs.map((tab) => (
        <Style.TabPanel
          key={tab.value}
          role="tabpanel"
          id={`minimal-tabpanel-${generatedId}-${tab.value}`}
          aria-labelledby={`minimal-tab-${generatedId}-${tab.value}`}
          tabIndex={0}
          hidden={tab.value !== selected}
        >
          {tab.content}
        </Style.TabPanel>
      ))}
    </Style.TabsRoot>
  );
};

const normalizeDate = (value?: Date | string | null) => {
  if (!value) return null;
  if (value instanceof Date) {
    return Number.isNaN(value.getTime()) ? null : new Date(value);
  }
  const plainDate = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (plainDate) {
    const [, year, month, day] = plainDate;
    const parsed = new Date(Number(year), Number(month) - 1, Number(day));
    return parsed.getFullYear() === Number(year) && parsed.getMonth() === Number(month) - 1 && parsed.getDate() === Number(day)
      ? parsed
      : null;
  }
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? null : parsed;
};

export const MinimalDatePicker = ({
  value,
  onChange,
  minDate,
  maxDate,
  isDateDisabled,
  locale = "en-US",
  weekStartsOn = 1,
  placeholder = "Choose a date…",
  disabled = false,
  clearable = false,
  dateFormat = { weekday: "short", year: "numeric", month: "short", day: "numeric" },
  label,
  description,
  hint,
  error,
  ...props
}: MinimalDatePickerProps) => {
  const generatedId = useId();
  const triggerId = `minimal-date-picker-${generatedId}`;
  const hintId = hint ? `${triggerId}-hint` : undefined;
  const errorId = error ? `${triggerId}-error` : undefined;
  const selected = normalizeDate(value);
  const formatter = useMemo(() => new Intl.DateTimeFormat(locale, dateFormat), [locale, dateFormat]);
  const [open, setOpen] = useState(false);
  const anchorRef = useRef<HTMLSpanElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    const closeOnOutsidePointer = (event: PointerEvent) => {
      if (anchorRef.current && !anchorRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
        triggerRef.current?.focus();
      }
    };
    document.addEventListener("pointerdown", closeOnOutsidePointer);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOnOutsidePointer);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  return (
    <Style.Field data-minimal="DatePicker" {...props}>
      <Style.Label htmlFor={triggerId}>{label}</Style.Label>
      {description ? <Style.Description>{description}</Style.Description> : null}
      <Style.Anchor ref={anchorRef}>
        <Style.DateTrigger
          ref={triggerRef}
          id={triggerId}
          type="button"
          $placeholder={!selected}
          $invalid={Boolean(error)}
          disabled={disabled}
          aria-haspopup="dialog"
          aria-expanded={open}
          aria-invalid={Boolean(error) || undefined}
          aria-describedby={[hintId, errorId].filter(Boolean).join(" ") || undefined}
          onClick={() => setOpen((previous) => !previous)}
        >
          <Style.DateTriggerValue>{selected ? formatter.format(selected) : placeholder}</Style.DateTriggerValue>
          <Style.DateTriggerIcon aria-hidden>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
              <rect x="3" y="5" width="18" height="16" rx="2" stroke="currentColor" strokeWidth="1.8" />
              <path d="M8 3v4M16 3v4M3 10h18" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
            </svg>
          </Style.DateTriggerIcon>
        </Style.DateTrigger>
        {/*
          The field's name belongs to the trigger, which the <label htmlFor>
          above already names. The popover is a second thing — the date chooser
          — so it carries its own name rather than repeating the field's. It
          used to echo `label` on the dialog, again on a visually-hidden span,
          and a third time on the calendar inside, so assistive tech announced
          "Delivery date" three times for one field and `getByLabelText` could
          not identify the control.
        */}
        <Style.DatePanel
          $open={open}
          role="dialog"
          aria-label={typeof label === "string" ? `Choose ${label}` : "Choose a date"}
          aria-hidden={!open}
        >
          {open ? (
            <MinimalCalendar
              value={selected}
              onChange={(next) => {
                onChange(next);
                setOpen(false);
                triggerRef.current?.focus();
              }}
              minDate={minDate}
              maxDate={maxDate}
              isDateDisabled={isDateDisabled}
              locale={locale}
              weekStartsOn={weekStartsOn}
              showAdjacentDays={false}
            />
          ) : null}
        </Style.DatePanel>
      </Style.Anchor>
      {clearable && selected ? (
        <Style.ClearButton type="button" onClick={() => onChange(null)} disabled={disabled}>
          Clear date
        </Style.ClearButton>
      ) : null}
      {error ? (
        <Style.Message id={errorId} $error role="alert">{error}</Style.Message>
      ) : hint ? (
        <Style.Message id={hintId} $error={false}>{hint}</Style.Message>
      ) : null}
    </Style.Field>
  );
};

const timeToMinutes = (value: string) => {
  const match = /^(\d{2}):(\d{2})$/.exec(value);
  if (!match) return null;
  const hours = Number(match[1]);
  const minutes = Number(match[2]);
  if (hours > 23 || minutes > 59) return null;
  return hours * 60 + minutes;
};

export const buildMinimalTimeOptions = ({
  minTime = "00:00",
  maxTime = "23:59",
  intervalMinutes = 30,
  locale = "en-US",
  hour12,
}: Pick<MinimalTimePickerProps, "minTime" | "maxTime" | "intervalMinutes" | "locale" | "hour12"> = {}) => {
  const minimum = timeToMinutes(minTime) ?? 0;
  const maximum = timeToMinutes(maxTime) ?? 1439;
  const interval = Math.min(720, Math.max(1, Math.round(intervalMinutes)));
  const formatter = new Intl.DateTimeFormat(locale, { hour: "numeric", minute: "2-digit", hour12 });
  const options: MinimalOption<string>[] = [];
  for (let minutes = minimum; minutes <= maximum && options.length < 1440; minutes += interval) {
    const hours = Math.floor(minutes / 60);
    const minute = minutes % 60;
    const value = `${String(hours).padStart(2, "0")}:${String(minute).padStart(2, "0")}`;
    options.push({ value, label: formatter.format(new Date(2024, 0, 1, hours, minute)) });
  }
  return options;
};

export const MinimalTimePicker = ({
  value,
  onChange,
  label,
  hint,
  error,
  minTime = "00:00",
  maxTime = "23:59",
  intervalMinutes = 30,
  locale = "en-US",
  hour12,
  disabled,
  placeholder = "Choose a time…",
  ...props
}: MinimalTimePickerProps) => {
  const options = useMemo(
    () => buildMinimalTimeOptions({ minTime, maxTime, intervalMinutes, locale, hour12 }),
    [minTime, maxTime, intervalMinutes, locale, hour12]
  );
  return (
    <div data-minimal="TimePicker" {...props}>
      <MinimalDropdown
        options={options}
        value={value}
        onChange={onChange}
        label={label}
        hint={hint}
        error={error ?? (options.length === 0 ? "No times are available in this range." : undefined)}
        disabled={disabled || options.length === 0}
        placeholder={placeholder}
        searchable={options.length > 12}
      />
    </div>
  );
};
