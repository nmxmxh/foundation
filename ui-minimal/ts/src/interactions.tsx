import { Checkbox } from "@base-ui/react/checkbox";
import { NumberField } from "@base-ui/react/number-field";
import { Popover } from "@base-ui/react/popover";
import { Switch } from "@base-ui/react/switch";
import { Tabs } from "@base-ui/react/tabs";
import { type HTMLAttributes, type ReactNode, useId, useMemo, useState } from "react";
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

const Style = {
  Field: styled.div`
    display: grid;
    gap: ${({ theme }) => theme.spacing.xs};
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
    gap: ${({ theme }) => theme.spacing.sm};
    align-items: start;
    cursor: ${({ $disabled }) => ($disabled ? "not-allowed" : "pointer")};
    opacity: ${({ $disabled }) => ($disabled ? 0.56 : 1)};
  `,
  ChoiceCopy: styled.span`
    display: grid;
    gap: 2px;
    min-width: 0;
    padding-block: 4px;
  `,
  ChoiceTitle: styled.span`
    color: ${({ theme }) => theme.color.textPrimary};
    font-size: ${({ theme }) => theme.typography.bodySize};
    font-weight: ${({ theme }) => theme.typography.weightMedium};
  `,
  CheckboxRoot: styled(Checkbox.Root)`
    ${controlReset}
    ${focusRing}
    display: inline-grid;
    place-items: center;
    width: var(--minimal-control-min-target);
    height: var(--minimal-control-min-target);
    border: 1px solid ${({ theme }) => theme.color.borderStrong};
    border-radius: ${({ theme }) => theme.radius.sm};
    background: ${({ theme }) => theme.color.bgSurface};
    cursor: inherit;

    &[data-checked],
    &[data-indeterminate] {
      border-color: ${({ theme }) => theme.color.brand};
      background: ${({ theme }) => theme.color.brand};
      color: ${({ theme }) => theme.color.textInverse};
    }
  `,
  CheckboxIndicator: styled(Checkbox.Indicator)`
    font-size: 1rem;
    font-weight: ${({ theme }) => theme.typography.weightBold};
    line-height: 1;
  `,
  SwitchRoot: styled(Switch.Root)`
    ${controlReset}
    ${focusRing}
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
    cursor: inherit;
    transition:
      background-color 160ms cubic-bezier(0.22, 1, 0.36, 1),
      border-color 160ms cubic-bezier(0.22, 1, 0.36, 1);

    &[data-checked] {
      border-color: ${({ theme }) => theme.color.brand};
      background: ${({ theme }) => theme.color.brand};
    }
  `,
  SwitchThumb: styled(Switch.Thumb)`
    display: block;
    width: 20px;
    height: 20px;
    border-radius: 50%;
    background: ${({ theme }) => theme.color.bgSurface};
    border: 1px solid ${({ theme }) => theme.color.borderSubtle};
    box-shadow: 0 1px 3px rgba(28, 28, 30, 0.24);
    transition: transform 160ms cubic-bezier(0.22, 1, 0.36, 1);

    [data-checked] & {
      border-color: transparent;
      transform: translateX(18px);
    }

    @media (prefers-reduced-motion: reduce) {
      transition: none;
    }
  `,
  NumberRoot: styled(NumberField.Root)`
    display: grid;
    gap: ${({ theme }) => theme.spacing.xs};
  `,
  NumberGroup: styled(NumberField.Group)<{ $size: MinimalSize; $invalid: boolean }>`
    display: grid;
    grid-template-columns: var(--minimal-control-min-target) minmax(0, 1fr) var(--minimal-control-min-target);
    min-height: ${({ $size }) => `var(--minimal-control-height-${$size})`};
    overflow: hidden;
    border: 1px solid ${({ theme, $invalid }) => ($invalid ? theme.color.danger : theme.color.borderSubtle)};
    border-radius: ${({ theme }) => theme.radius.sm};
    background: ${({ theme }) => theme.color.bgSurface};

    &:focus-within {
      border-color: ${({ theme, $invalid }) => ($invalid ? theme.color.danger : theme.color.borderFocus)};
      box-shadow: 0 0 0 ${({ theme }) => theme.focus.ringWidth}
        ${({ theme, $invalid }) => ($invalid ? theme.color.dangerSoft : theme.color.brandSoft)};
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
  NumberInput: styled(NumberField.Input)`
    min-width: 0;
    width: 100%;
    border: 0;
    outline: 0;
    background: transparent;
    color: ${({ theme }) => theme.color.textPrimary};
    padding: 0 ${({ theme }) => theme.spacing.sm};
    text-align: center;
  `,
  TabsRoot: styled(Tabs.Root)`
    display: grid;
    gap: ${({ theme }) => theme.spacing.md};
  `,
  TabsList: styled(Tabs.List)`
    display: flex;
    gap: ${({ theme }) => theme.spacing.xs};
    overflow-x: auto;
    padding: ${({ theme }) => theme.spacing.xs};
    border: 1px solid ${({ theme }) => theme.color.borderSubtle};
    border-radius: ${({ theme }) => theme.radius.md};
    background: ${({ theme }) => theme.color.bgSurfaceAlt};

    &[data-orientation="vertical"] {
      flex-direction: column;
    }
  `,
  Tab: styled(Tabs.Tab)`
    ${controlReset}
    ${focusRing}
    min-height: var(--minimal-control-min-target);
    padding: 8px 14px;
    border-radius: ${({ theme }) => theme.radius.sm};
    color: ${({ theme }) => theme.color.textSecondary};
    cursor: pointer;
    white-space: nowrap;

    &[data-active] {
      background: ${({ theme }) => theme.color.bgSurface};
      color: ${({ theme }) => theme.color.textPrimary};
      box-shadow: ${({ theme }) => theme.shadow.subtle};
    }

    &:disabled {
      cursor: not-allowed;
      opacity: 0.48;
    }
  `,
  TabPanel: styled(Tabs.Panel)`
    min-width: 0;
    outline: none;
  `,
  DateTrigger: styled(Popover.Trigger)<{ $placeholder: boolean; $invalid: boolean }>`
    ${controlReset}
    ${focusRing}
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: ${({ theme }) => theme.spacing.sm};
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
  DatePositioner: styled(Popover.Positioner)`
    z-index: ${({ theme }) => theme.zIndex.dropdown};
    outline: none;
  `,
  DatePopup: styled(Popover.Popup)`
    width: min(calc(100vw - (2 * var(--minimal-overlay-viewport-gutter))), 368px);
    max-height: var(--minimal-overlay-max-height);
    overflow: auto;
    border: 1px solid ${({ theme }) => theme.color.borderStrong};
    border-radius: ${({ theme }) => theme.radius.md};
    background: ${({ theme }) => theme.color.bgSurface};
    box-shadow: ${({ theme }) => theme.shadow.floating};
    transform-origin: var(--transform-origin);
    transition:
      transform 180ms cubic-bezier(0.22, 1, 0.36, 1),
      opacity 140ms ease-out;

    &[data-starting-style],
    &[data-ending-style] {
      opacity: 0;
      transform: scale(0.98);
    }

    & [data-minimal="Calendar"] {
      border: 0;
      border-radius: inherit;
    }

    @media (prefers-reduced-motion: reduce) {
      transition: none;
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
}: MinimalCheckboxProps) => (
  <div data-minimal="Checkbox" {...props}>
    <Style.ChoiceLabel $disabled={disabled}>
      <Style.CheckboxRoot
        checked={checked}
        defaultChecked={defaultChecked}
        onCheckedChange={(next) => onCheckedChange?.(next)}
        indeterminate={indeterminate}
        disabled={disabled}
        readOnly={readOnly}
        required={required}
        name={name}
        value={value}
      >
        <Style.CheckboxIndicator>{indeterminate ? "−" : "✓"}</Style.CheckboxIndicator>
      </Style.CheckboxRoot>
      <Style.ChoiceCopy>
        <Style.ChoiceTitle>{label}</Style.ChoiceTitle>
        <ChoiceMessage description={description} hint={hint} error={error} />
      </Style.ChoiceCopy>
    </Style.ChoiceLabel>
  </div>
);

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
      <Style.SwitchRoot
        checked={checked}
        defaultChecked={defaultChecked}
        onCheckedChange={(next) => onCheckedChange?.(next)}
        disabled={disabled}
        readOnly={readOnly}
        required={required}
        name={name}
        value={value}
      >
        <Style.SwitchThumb />
      </Style.SwitchRoot>
      <Style.ChoiceCopy>
        <Style.ChoiceTitle>{label}</Style.ChoiceTitle>
        <ChoiceMessage description={description} hint={hint} error={error} />
      </Style.ChoiceCopy>
    </Style.ChoiceLabel>
  </div>
);

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
  format,
  locale,
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

  return (
    <Style.NumberRoot
      data-minimal="NumberField"
      id={id}
      value={value}
      defaultValue={defaultValue}
      onValueChange={(next) => onValueChange?.(next)}
      onValueCommitted={(next) => onValueCommitted?.(next)}
      min={min}
      max={max}
      step={step}
      smallStep={smallStep}
      largeStep={largeStep}
      format={format}
      locale={locale}
      name={name}
      required={required}
      disabled={disabled}
      readOnly={readOnly}
      allowWheelScrub={allowWheelScrub}
    >
      <Style.Label htmlFor={id}>{label}</Style.Label>
      {description ? <Style.Description>{description}</Style.Description> : null}
      <Style.NumberGroup $size={inputSize} $invalid={Boolean(error)}>
        <NumberField.Decrement render={<Style.NumberButton aria-label={decrementLabel}>−</Style.NumberButton>} />
        <Style.NumberInput
          placeholder={placeholder}
          aria-invalid={Boolean(error) || undefined}
          aria-describedby={[hintId, errorId].filter(Boolean).join(" ") || undefined}
        />
        <NumberField.Increment render={<Style.NumberButton aria-label={incrementLabel}>+</Style.NumberButton>} />
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
}: MinimalTabsProps<T>) => (
  <Style.TabsRoot
    data-minimal="Tabs"
    value={value}
    defaultValue={defaultValue}
    orientation={orientation}
    onValueChange={(next) => onValueChange?.(next as T)}
    {...props}
  >
    <Style.TabsList aria-label={ariaLabel}>
      {tabs.map((tab) => (
        <Style.Tab key={tab.value} value={tab.value} disabled={tab.disabled}>
          {tab.label}
        </Style.Tab>
      ))}
    </Style.TabsList>
    {tabs.map((tab) => (
      <Style.TabPanel key={tab.value} value={tab.value}>
        {tab.content}
      </Style.TabPanel>
    ))}
  </Style.TabsRoot>
);

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

  return (
    <Style.Field data-minimal="DatePicker" {...props}>
      <Style.Label htmlFor={triggerId}>{label}</Style.Label>
      {description ? <Style.Description>{description}</Style.Description> : null}
      <Popover.Root open={open} onOpenChange={setOpen}>
        <Style.DateTrigger
          id={triggerId}
          $placeholder={!selected}
          $invalid={Boolean(error)}
          disabled={disabled}
          aria-invalid={Boolean(error) || undefined}
          aria-describedby={[hintId, errorId].filter(Boolean).join(" ") || undefined}
        >
          <Style.DateTriggerValue>{selected ? formatter.format(selected) : placeholder}</Style.DateTriggerValue>
          <Style.DateTriggerIcon aria-hidden>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
              <rect x="3" y="5" width="18" height="16" rx="2" stroke="currentColor" strokeWidth="1.8" />
              <path d="M8 3v4M16 3v4M3 10h18" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
            </svg>
          </Style.DateTriggerIcon>
        </Style.DateTrigger>
        <Popover.Portal>
          <Style.DatePositioner side="bottom" align="start" sideOffset={8} collisionPadding={16}>
            <Style.DatePopup>
              <Popover.Title render={<Style.VisuallyHidden />}>{label}</Popover.Title>
              <MinimalCalendar
                value={selected}
                onChange={(next) => {
                  onChange(next);
                  setOpen(false);
                }}
                minDate={minDate}
                maxDate={maxDate}
                isDateDisabled={isDateDisabled}
                locale={locale}
                weekStartsOn={weekStartsOn}
                showAdjacentDays={false}
                aria-label={typeof label === "string" ? label : "Choose a date"}
              />
            </Style.DatePopup>
          </Style.DatePositioner>
        </Popover.Portal>
      </Popover.Root>
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
