// Public build-time URL only. Never put credentials in VITE_* variables.
export const apiBaseUrl = (import.meta.env.VITE_API_BASE_URL ?? "").replace(
  /\/+$/,
  "",
);
export const referenceWorkpaperUrl = `${apiBaseUrl}/v1/reference/prepaid`;
