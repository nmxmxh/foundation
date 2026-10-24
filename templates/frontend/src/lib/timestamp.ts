/**
 * Shared timestamp parsing, normalization, and ordering utilities.
 * Anchored to @ovasabi/frontend-kit contracts.
 */
export {
  compareTimestamps,
  extractSubMsFraction,
  formatRFC3339UTC,
  isAfterTimestamp,
  normalizeTimestampString,
  parseTimestamp,
  type CompareTimestampOptions,
  type TimestampNullsOrder,
  type TimestampOrderDirection,
} from '@ovasabi/frontend-kit';
