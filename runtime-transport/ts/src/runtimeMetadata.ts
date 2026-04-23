const SESSION_PREFIX = "session_";

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === "object" && !Array.isArray(value);

export const normalizeRuntimeString = (value: unknown): string => (typeof value === "string" ? value.trim() : "");

export const isPlaceholderRuntimeValue = (value: string): boolean => value === "" || value === "loading";

export const looksLikeJWT = (value: string): boolean => {
  const trimmed = normalizeRuntimeString(value);
  if (!trimmed) {
    return false;
  }
  const parts = trimmed.split(".");
  return parts.length === 3 && parts.every((part) => part.length > 0);
};

export const stripBearerPrefix = (value: string): string => {
  const trimmed = normalizeRuntimeString(value);
  return trimmed.toLowerCase().startsWith("bearer ") ? trimmed.slice(7).trim() : trimmed;
};

export const createRuntimeSessionID = (): string => {
  return `${SESSION_PREFIX}${Date.now()}_${Math.random().toString(36).slice(2, 11)}`;
};

export const pickRuntimeSessionID = (...candidates: unknown[]): string => {
  for (const candidate of candidates) {
    const normalized = normalizeRuntimeString(candidate);
    if (isPlaceholderRuntimeValue(normalized)) {
      continue;
    }
    if (looksLikeJWT(stripBearerPrefix(normalized))) {
      continue;
    }
    return normalized;
  }

  return "";
};

export const resolveRuntimeSessionID = (...candidates: unknown[]): string => {
  return pickRuntimeSessionID(...candidates) || createRuntimeSessionID();
};

export const resolveRuntimeAuthToken = (...candidates: unknown[]): string => {
  for (const candidate of candidates) {
    const normalized = stripBearerPrefix(normalizeRuntimeString(candidate));
    if (!isPlaceholderRuntimeValue(normalized)) {
      return normalized;
    }
  }
  return "";
};

export const mergeGlobalContextExtras = (
  existing: unknown,
  injected: Record<string, unknown>
): Record<string, unknown> => {
  const merged: Record<string, unknown> = isRecord(existing) ? { ...existing } : {};
  Object.entries(injected).forEach(([key, value]) => {
    if (value === undefined || value === null || value === "") {
      delete merged[key];
      return;
    }
    merged[key] = value;
  });
  return merged;
};
