// A close's tabs, in order (features/closes/ClientClose.tsx). Its loader (routes.tsx) reads what the open tab shows and
// treats an unknown tab as Overview, as the page does.
export const closeTabs = [
  ["overview", "Overview"],
  ["books", "Books"],
  ["workpapers", "Workpapers"],
  ["evidence", "Documents"],
  ["requests", "Requests"],
  ["reports", "Reports"],
  ["cash", "Cash plan"],
  ["signoff", "Sign-off"],
  ["settings", "Settings"],
] as const;
export type CloseTab = (typeof closeTabs)[number][0];
export const closeTab = (asked: string | null): CloseTab => closeTabs.find(([key]) => key === asked)?.[0] ?? "overview";
