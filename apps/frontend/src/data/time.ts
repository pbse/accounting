import { useQuery } from "@tanstack/react-query";
import { workspaceQuery } from "./queries";

// Dates read the same on the server and in the browser, so a server-rendered page hydrates without a mismatch: one
// locale, and the person's own time zone from their settings (UTC until those are read). The browser's own locale
// and zone would differ from the server's.
export function useDates() {
  const zone = useQuery(workspaceQuery<{ time_zone?: string }>("/me")).data?.time_zone || "UTC";
  const format = (options: Intl.DateTimeFormatOptions, timeZone = zone) => {
    let formatter: Intl.DateTimeFormat;
    try {
      formatter = new Intl.DateTimeFormat("en-US", { ...options, timeZone });
    } catch {
      formatter = new Intl.DateTimeFormat("en-US", { ...options, timeZone: "UTC" });
    }
    return (value: string | number | Date) => formatter.format(new Date(value));
  };
  // A calendar date (YYYY-MM-DD, such as a due date) is the same day everywhere: it is read as that day, not as an
  // instant in some zone.
  const calendar = (options: Intl.DateTimeFormatOptions) => {
    const day = format(options, "UTC");
    return (value: string) => day(`${value.slice(0, 10)}T00:00:00Z`);
  };
  return {
    dateTime: format({ dateStyle: "medium", timeStyle: "short" }),
    date: format({ dateStyle: "medium" }),
    time: format({ hour: "numeric", minute: "2-digit" }),
    dayMonth: format({ day: "numeric", month: "short" }),
    calendarDay: calendar({ weekday: "short", day: "numeric", month: "short" }),
    calendarDate: calendar({ dateStyle: "medium" }),
  };
}

// A calendar month (YYYY-MM), such as a close's period, named the same everywhere: "September 2026".
const monthFormat = new Intl.DateTimeFormat("en-US", { month: "long", year: "numeric", timeZone: "UTC" });
export const monthName = (period: string) => monthFormat.format(new Date(`${period.slice(0, 7)}-01T00:00:00Z`));
const monthShortFormat = new Intl.DateTimeFormat("en-US", { month: "short", year: "numeric", timeZone: "UTC" });
// The same, short: "Sep 2026".
export const monthShort = (period: string) => monthShortFormat.format(new Date(`${period.slice(0, 7)}-01T00:00:00Z`));

// A UTC instant's day and short month, as a close's plan states it: "8 Oct" style, in one locale.
const utcDayMonth = new Intl.DateTimeFormat("en-US", { day: "numeric", month: "short", timeZone: "UTC" });
export const utcDay = (value: string | number | Date) => utcDayMonth.format(new Date(value));

// The clock a render reads: the server sets it as it renders and sends it with the page, so the browser's first render
// counts days and ages from the same instant; it then moves on every minute. Reading Date.now() during a render would
// differ between the two.
export const clockKey = ["clock"] as const;
export function useNow(): number {
  return useQuery({ queryKey: clockKey, queryFn: () => Date.now(), initialData: () => Date.now(), staleTime: 60_000, refetchInterval: 60_000 }).data;
}

// useToday is the person's calendar date (YYYY-MM-DD) in their own time zone, by the render's clock.
export function useToday(): string {
  const now = useNow();
  const zone = useQuery(workspaceQuery<{ time_zone?: string }>("/me")).data?.time_zone || "UTC";
  try {
    return new Intl.DateTimeFormat("en-CA", { timeZone: zone, year: "numeric", month: "2-digit", day: "2-digit" }).format(now);
  } catch {
    return new Date(now).toISOString().slice(0, 10);
  }
}

// useHour is the hour of the person's day (0–23) in their own time zone, by the render's clock: the greeting's.
export function useHour(): number {
  const now = useNow();
  const zone = useQuery(workspaceQuery<{ time_zone?: string }>("/me")).data?.time_zone || "UTC";
  try {
    return Number(new Intl.DateTimeFormat("en-US", { timeZone: zone, hour: "numeric", hourCycle: "h23" }).format(now));
  } catch {
    return new Date(now).getUTCHours();
  }
}

// daysBetween counts calendar days from one YYYY-MM-DD date to another.
export const daysBetween = (from: string, to: string) => Math.round((Date.parse(`${to.slice(0, 10)}T00:00:00Z`) - Date.parse(`${from.slice(0, 10)}T00:00:00Z`)) / 86_400_000);
