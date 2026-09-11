// ragError.ts turns raw extraction error strings into human sentences.
// The backend persists the literal Go error (e.g. "context deadline exceeded",
// "extract HTTP 429: ...") on the job row; showing that verbatim is why users
// saw a bare 出错 with no idea what went wrong. Mapping happens here, on the
// frontend, so the DB keeps the precise machine-readable cause while people
// get an actionable phrase plus the original text when nothing matches.

import type { Translator } from "./i18n";

export function humanizeRagError(raw: string | undefined | null, t: Translator): string {
  if (!raw) return "";
  const s = raw.toLowerCase();
  if (s.includes("no failed chunks")) {
    return t("cowork.ragRetryNone");
  }
  if (s.includes("context deadline exceeded") || s.includes("timeout") || s.includes("timed out")) {
    return t("cowork.ragErrTimeout");
  }
  // HTTP statuses are matched with their "http" prefix — a bare "429" would
  // false-positive on chunk counts and timestamps inside raw errors.
  if (s.includes("http 429") || s.includes("rate limit")) {
    return t("cowork.ragErrRateLimit");
  }
  if (s.includes("http 401") || s.includes("http 403") || s.includes("unauthorized")) {
    return t("cowork.ragErrAuth");
  }
  if (s.includes("http 402") || s.includes("insufficient quota") || s.includes("quota exceeded")) {
    return t("cowork.ragErrQuota");
  }
  if (s.includes("http 5") || s.includes("bad gateway") || s.includes("server error")) {
    return t("cowork.ragErrServer");
  }
  if (s.includes("api key") || s.includes("no llm model configured")) {
    return t("cowork.ragErrKey");
  }
  if (s.includes("no such file") || s.includes("cannot find") || s.includes("access is denied") || s.includes("permission denied")) {
    return t("cowork.ragErrFile");
  }
  return raw;
}
