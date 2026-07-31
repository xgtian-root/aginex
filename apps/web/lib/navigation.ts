const maxRedirectLength = 2048;

export function safeLocalRedirect(
  value: string | null | undefined,
  fallback = "/dashboard",
): string {
  const candidate = value?.trim() ?? "";
  if (
    candidate.length === 0 ||
    candidate.length > maxRedirectLength ||
    !candidate.startsWith("/") ||
    candidate.startsWith("//") ||
    candidate.includes("\\") ||
    hasControlCharacters(candidate)
  ) {
    return fallback;
  }

  let decoded = candidate;
  try {
    for (let attempt = 0; attempt < 2; attempt += 1) {
      const next = decodeURIComponent(decoded);
      if (next === decoded) break;
      decoded = next;
    }
  } catch {
    return fallback;
  }

  if (
    !decoded.startsWith("/") ||
    decoded.startsWith("//") ||
    decoded.includes("\\") ||
    hasControlCharacters(decoded)
  ) {
    return fallback;
  }
  return candidate;
}

function hasControlCharacters(value: string) {
  for (const character of value) {
    const code = character.charCodeAt(0);
    if (code <= 0x1f || code === 0x7f) {
      return true;
    }
  }
  return false;
}
