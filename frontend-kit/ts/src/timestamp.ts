export type TimestampOrderDirection = "asc" | "desc";
export type TimestampNullsOrder = "first" | "last";

export interface CompareTimestampOptions {
  direction?: TimestampOrderDirection;
  nullsOrder?: TimestampNullsOrder;
}

const spaceDatePattern = /^(\d{4}-\d{2}-\d{2})[ ](\d{2}:\d{2}:\d{2}(?:\.\d+)?)(.*)$/;
const shortTzPattern = /(?:T\S*|:\d{2})([+-]\d{2})$/;
const fractionPattern = /\.(\d+)/;

/**
 * Normalizes a raw timestamp string into an ISO-8601 string compatible with Date.parse.
 * Replaces the space separator with 'T' and repairs two-digit timezone offsets.
 */
export const normalizeTimestampString = (raw: string): string => {
  const trimmed = raw.trim();
  if (trimmed === "") {
    return "";
  }

  let formatted = trimmed;
  const match = spaceDatePattern.exec(formatted);
  if (match) {
    const [, date, time, rest] = match;
    formatted = `${date}T${time}${rest}`;
  }

  if (shortTzPattern.test(formatted)) {
    formatted = `${formatted}:00`;
  }

  return formatted;
};

/**
 * Parses any timestamp input into numeric epoch milliseconds.
 * Returns null if the value is missing or invalid.
 */
export const parseTimestamp = (value: unknown): number | null => {
  if (value === null || value === undefined) {
    return null;
  }

  if (typeof value === "number") {
    return Number.isFinite(value) ? value : null;
  }

  if (value instanceof Date) {
    const time = value.getTime();
    return Number.isNaN(time) ? null : time;
  }

  if (typeof value === "string") {
    const normalized = normalizeTimestampString(value);
    if (normalized === "") {
      return null;
    }
    const parsed = Date.parse(normalized);
    return Number.isNaN(parsed) ? null : parsed;
  }

  return null;
};

/**
 * Extracts sub-millisecond fractional digits beyond three decimal places.
 * Returns a normalized integer representing microsecond precision (0 to 999999).
 */
export const extractSubMsFraction = (value: unknown): number => {
  if (typeof value !== "string") {
    return 0;
  }

  const match = fractionPattern.exec(value);
  if (!match) {
    return 0;
  }

  const digits = match[1];
  if (digits.length <= 3) {
    return 0;
  }

  const subDigits = digits.slice(3, 9).padEnd(6, "0");
  const parsed = parseInt(subDigits, 10);
  return Number.isNaN(parsed) ? 0 : parsed;
};

/**
 * Compares two timestamps numerically using epoch milliseconds.
 * Applies sub-millisecond tie-breaking before record identifier comparison.
 */
export const compareTimestamps = (
  aDate: unknown,
  bDate: unknown,
  aId?: string,
  bId?: string,
  options?: CompareTimestampOptions
): number => {
  const direction = options?.direction ?? "asc";
  const nullsOrder = options?.nullsOrder ?? "last";
  const multiplier = direction === "desc" ? -1 : 1;

  const aTime = parseTimestamp(aDate);
  const bTime = parseTimestamp(bDate);

  if (aTime === null && bTime === null) {
    if (aId !== undefined || bId !== undefined) {
      return (aId ?? "").localeCompare(bId ?? "");
    }
    return 0;
  }

  if (aTime === null) {
    return nullsOrder === "first" ? -1 : 1;
  }

  if (bTime === null) {
    return nullsOrder === "first" ? 1 : -1;
  }

  if (aTime !== bTime) {
    return (aTime - bTime) * multiplier;
  }

  const aSub = extractSubMsFraction(aDate);
  const bSub = extractSubMsFraction(bDate);
  if (aSub !== bSub) {
    return (aSub - bSub) * multiplier;
  }

  if (aId !== undefined || bId !== undefined) {
    return (aId ?? "").localeCompare(bId ?? "");
  }

  return 0;
};

/**
 * Returns true if timestamp a occurs strictly after timestamp b.
 * Accounts for sub-millisecond fractional precision.
 */
export const isAfterTimestamp = (aDate: unknown, bDate: unknown): boolean => {
  const aTime = parseTimestamp(aDate);
  const bTime = parseTimestamp(bDate);

  if (aTime === null || bTime === null) {
    return false;
  }

  if (aTime !== bTime) {
    return aTime > bTime;
  }

  return extractSubMsFraction(aDate) > extractSubMsFraction(bDate);
};

/**
 * Formats a valid timestamp input into canonical ISO-8601 UTC format.
 * Returns an empty string for invalid inputs.
 */
export const formatRFC3339UTC = (value: unknown): string => {
  const time = parseTimestamp(value);
  if (time === null) {
    return "";
  }
  return new Date(time).toISOString();
};
