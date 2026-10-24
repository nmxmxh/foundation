import { describe, expect, it } from "vitest";
import {
  compareTimestamps,
  extractSubMsFraction,
  formatRFC3339UTC,
  isAfterTimestamp,
  normalizeTimestampString,
  parseTimestamp,
} from "./timestamp";

describe("timestamp utilities", () => {
  describe("normalizeTimestampString", () => {
    it("converts PostgreSQL space-separated timestamp to ISO format", () => {
      const input = "2026-10-24 22:58:00.123456+00";
      expect(normalizeTimestampString(input)).toBe("2026-10-24T22:58:00.123456+00:00");
    });

    it("repairs short two-digit timezone offsets", () => {
      expect(normalizeTimestampString("2026-10-24T22:58:00+00")).toBe("2026-10-24T22:58:00+00:00");
      expect(normalizeTimestampString("2026-10-24 22:58:00+02")).toBe("2026-10-24T22:58:00+02:00");
    });

    it("handles whitespace and empty strings", () => {
      expect(normalizeTimestampString("")).toBe("");
      expect(normalizeTimestampString("   ")).toBe("");
    });
  });

  describe("parseTimestamp", () => {
    it("parses ISO strings", () => {
      const iso = "2026-10-24T22:58:00.000Z";
      expect(parseTimestamp(iso)).toBe(Date.parse(iso));
    });

    it("parses PostgreSQL space-separated strings correctly", () => {
      const pgString = "2026-10-24 22:58:00+00";
      const expected = Date.parse("2026-10-24T22:58:00Z");
      expect(parseTimestamp(pgString)).toBe(expected);
    });

    it("accepts numbers and Date objects", () => {
      const now = Date.now();
      expect(parseTimestamp(now)).toBe(now);
      expect(parseTimestamp(new Date(now))).toBe(now);
    });

    it("returns null for invalid inputs", () => {
      expect(parseTimestamp(null)).toBeNull();
      expect(parseTimestamp(undefined)).toBeNull();
      expect(parseTimestamp("invalid-date")).toBeNull();
      expect(parseTimestamp(NaN)).toBeNull();
      expect(parseTimestamp(new Date("invalid"))).toBeNull();
    });
  });

  describe("extractSubMsFraction", () => {
    it("returns 0 when fraction is three digits or fewer", () => {
      expect(extractSubMsFraction("2026-10-24T22:58:00.123Z")).toBe(0);
      expect(extractSubMsFraction("2026-10-24T22:58:00.12Z")).toBe(0);
      expect(extractSubMsFraction("2026-10-24T22:58:00Z")).toBe(0);
    });

    it("extracts microsecond values beyond milliseconds", () => {
      expect(extractSubMsFraction("2026-10-24T22:58:00.123456Z")).toBe(456000);
      expect(extractSubMsFraction("2026-10-24 22:58:00.123789+00")).toBe(789000);
      expect(extractSubMsFraction("2026-10-24T22:58:00.123456789Z")).toBe(456789);
    });
  });

  describe("compareTimestamps (Lexicographical Trap Resolution)", () => {
    it("resolves the ASCII space versus T comparison trap", () => {
      const spaceTime = "2026-10-24 22:58:00+00"; // 22:58 (later)
      const isoTime = "2026-10-24T22:50:00Z"; // 22:50 (earlier)

      // Raw localeCompare evaluates space (ASCII 32) as less than 'T' (ASCII 84)
      expect(spaceTime.localeCompare(isoTime)).toBe(-1);

      // compareTimestamps correctly identifies 22:58 as after 22:50
      expect(compareTimestamps(spaceTime, isoTime)).toBeGreaterThan(0);
      expect(compareTimestamps(isoTime, spaceTime)).toBeLessThan(0);
    });

    it("orders message collections chronologically regardless of format discrepancies", () => {
      const messages = [
        { id: "m3", time: "2026-10-24 22:58:00.123456+00" }, // Latest
        { id: "m1", time: "2026-10-24T22:45:00.000Z" }, // Earliest
        { id: "m2", time: "2026-10-24T22:50:00.000Z" }, // Middle
      ];

      const sorted = [...messages].sort((a, b) => compareTimestamps(a.time, b.time, a.id, b.id));
      expect(sorted.map((m) => m.id)).toEqual(["m1", "m2", "m3"]);
    });

    it("resolves ties using sub-millisecond precision before record IDs", () => {
      const earlier = "2026-10-24T22:58:00.123400Z";
      const later = "2026-10-24T22:58:00.123800Z";

      expect(compareTimestamps(earlier, later, "id_z", "id_a")).toBeLessThan(0);
      expect(compareTimestamps(later, earlier, "id_a", "id_z")).toBeGreaterThan(0);
    });

    it("falls back to record ID when timestamps and fractions are identical", () => {
      const time = "2026-10-24T22:58:00.123456Z";
      expect(compareTimestamps(time, time, "id_a", "id_b")).toBeLessThan(0);
      expect(compareTimestamps(time, time, "id_b", "id_a")).toBeGreaterThan(0);
      expect(compareTimestamps(time, time, "id_a", "id_a")).toBe(0);
    });

    it("places null or in-flight messages at the end by default", () => {
      const confirmed = { id: "c1", time: "2026-10-24T22:50:00Z" };
      const pending = { id: "p1", time: undefined };

      const list = [pending, confirmed];
      const sorted = [...list].sort((a, b) => compareTimestamps(a.time, b.time, a.id, b.id));
      expect(sorted).toEqual([confirmed, pending]);
    });

    it("respects nullsOrder: first when specified", () => {
      const confirmed = { id: "c1", time: "2026-10-24T22:50:00Z" };
      const pending = { id: "p1", time: undefined };

      const list = [confirmed, pending];
      const sorted = [...list].sort((a, b) =>
        compareTimestamps(a.time, b.time, a.id, b.id, { nullsOrder: "first" })
      );
      expect(sorted).toEqual([pending, confirmed]);
    });

    it("supports reverse chronological sorting with direction: desc", () => {
      const messages = [
        { id: "m1", time: "2026-10-24T22:45:00Z" },
        { id: "m2", time: "2026-10-24T22:55:00Z" },
      ];

      const sorted = [...messages].sort((a, b) =>
        compareTimestamps(a.time, b.time, a.id, b.id, { direction: "desc" })
      );
      expect(sorted.map((m) => m.id)).toEqual(["m2", "m1"]);
    });
  });

  describe("isAfterTimestamp (Read Marks & Unread Counters)", () => {
    it("correctly evaluates read markers when formats differ", () => {
      const readMark = "2026-10-24T22:50:00Z";
      const spaceMessage = "2026-10-24 22:58:00+00";

      // Message at 22:58 was sent after read mark at 22:50
      expect(isAfterTimestamp(spaceMessage, readMark)).toBe(true);
      expect(isAfterTimestamp(readMark, spaceMessage)).toBe(false);
    });

    it("compares sub-millisecond timestamps for read markers", () => {
      const mark = "2026-10-24T22:50:00.123400Z";
      const msg = "2026-10-24T22:50:00.123800Z";

      expect(isAfterTimestamp(msg, mark)).toBe(true);
      expect(isAfterTimestamp(mark, msg)).toBe(false);
    });

    it("returns false when inputs are missing", () => {
      expect(isAfterTimestamp(null, "2026-10-24T22:50:00Z")).toBe(false);
      expect(isAfterTimestamp("2026-10-24T22:50:00Z", null)).toBe(false);
    });
  });

  describe("formatRFC3339UTC", () => {
    it("formats space-separated timestamp as canonical ISO-8601 UTC", () => {
      const formatted = formatRFC3339UTC("2026-10-24 22:58:00+00");
      expect(formatted).toBe("2026-10-24T22:58:00.000Z");
    });

    it("converts local or offset timestamps to UTC", () => {
      const formatted = formatRFC3339UTC("2026-10-24 22:58:00+02:00");
      expect(formatted).toBe("2026-10-24T20:58:00.000Z");
    });

    it("returns empty string for invalid inputs", () => {
      expect(formatRFC3339UTC("invalid")).toBe("");
      expect(formatRFC3339UTC(null)).toBe("");
    });
  });
});
