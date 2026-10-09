import { useQuery } from "@tanstack/react-query";
import { workspaceQuery } from "./queries";

// Dates read the same on the server and in the browser, so a server-rendered page hydrates without a mismatch: one
// locale, and the person's own time zone from their settings (UTC until those are read). The browser's own locale
// and zone would differ from the server's.
export function useDates() {
  const zone = useQuery(workspaceQuery<{ time_zone?: string }>("/me")).data?.time_zone || "UTC";
  const format = (options: Intl.DateTimeFormatOptions) => {
    let formatter: Intl.DateTimeFormat;
    try {
      formatter = new Intl.DateTimeFormat("en-US", { ...options, timeZone: zone });
    } catch {
      formatter = new Intl.DateTimeFormat("en-US", { ...options, timeZone: "UTC" });
    }
    return (value: string | number | Date) => formatter.format(new Date(value));
  };
  return { dateTime: format({ dateStyle: "medium", timeStyle: "short" }), date: format({ dateStyle: "medium" }) };
}
