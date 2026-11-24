const SECRET_KEY = /(token|secret|password|passwd|api[-_]?key|authorization|credential|cookie)/i;
const SECRET_VALUE = /\b(apk_[A-Za-z0-9_-]{8,}|Bearer\s+[A-Za-z0-9._~+/=-]{12,})\b/g;

export function redact(value: unknown, depth = 0): unknown {
  if (depth > 12) return "[depth limit]";
  if (typeof value === "string")
    return value.replace(SECRET_VALUE, (match) => `${match.slice(0, 6)}…[redacted]`);
  if (Array.isArray(value)) return value.slice(0, 500).map((item) => redact(item, depth + 1));
  if (value && typeof value === "object") {
    const result: Record<string, unknown> = {};
    for (const [key, item] of Object.entries(value as Record<string, unknown>).slice(0, 500)) {
      result[key] = SECRET_KEY.test(key) ? "[redacted]" : redact(item, depth + 1);
    }
    return result;
  }
  return value;
}

export function redactedJson(value: unknown): string {
  if (value === undefined) return "";
  try {
    return JSON.stringify(redact(value), null, 2);
  } catch {
    return String(value);
  }
}

export function summarize(
  value: unknown,
  keys: string[] = ["topic", "query", "prompt", "message"],
): string {
  if (!value || typeof value !== "object") return "";
  const record = value as Record<string, unknown>;
  for (const key of keys) {
    if (typeof record[key] === "string") return `${key}: ${record[key]}`;
  }
  return "";
}
