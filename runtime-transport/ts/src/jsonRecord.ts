/**
 * JsonRecord is the one name for a JSON object at the codec boundary:
 * envelope extras, metadata, decoded wire bodies. Typed contracts replace it
 * inside product code; it stays only where bytes become objects.
 */
export type JsonRecord = Record<string, unknown>;

/** isJsonRecord reports a non-null, non-array object. */
export const isJsonRecord = (value: unknown): value is JsonRecord =>
  value !== null && typeof value === "object" && !Array.isArray(value);
