const paths = {
  work: "M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z",
  clients: "M16 20v-1.5a3.5 3.5 0 0 0-3.5-3.5h-5A3.5 3.5 0 0 0 4 18.5V20M10 11a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7M20 20v-1.5a3.5 3.5 0 0 0-2.5-3.35M15.5 4.2a3.5 3.5 0 0 1 0 6.6",
  prepaid: "M6 3h9l4 4v14H6zM15 3v4h4M9 12h7M9 16h7",
  accrual: "M12 7v5l3 2M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0",
  loan: "M3 10h18M5 10v8m4-8v8m6-8v8m4-8v8M3 21h18M12 3l9 5H3l9-5",
  owner: "M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zm-7 9a7 7 0 0 1 14 0",
  reconcile: "M4 7h13M4 7l3-3M4 7l3 3M20 17H7M20 17l-3-3M20 17l-3 3",
  stripe: "M3 6h18v12H3zM3 10h18M7 15h4",
  firm: "M4 21V9l8-5 8 5v12M9 21v-6h6v6M4 21h16",
  book: "M4 5a2 2 0 0 1 2-2h13v16H6a2 2 0 0 0-2 2zM4 19V5",
  search: "M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14M20 20l-4-4",
  agent: "M12 3l1.8 4.9L19 9.7l-5.2 1.8L12 16.5l-1.8-5L5 9.7l5.2-1.8zM19 16l.8 2.2L22 19l-2.2.8L19 22l-.8-2.2L16 19l2.2-.8z",
  decision: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18M12 8v5M12 16.5v.5",
  review: "M9 12l2 2 4-4M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18",
  waiting: "M4 6h16v12H4zM4 7l8 6 8-6",
  blocked: "M12 3l9.5 17h-19zM12 10v4M12 17v.5",
  back: "M15 18l-6-6 6-6",
  arrow: "M5 12h14M13 6l6 6-6 6",
  file: "M6 3h9l4 4v14H6zM15 3v4h4",
  measure: "M5 20V10M10 20V4M15 20v-7M20 20V8",
} as const;

export type IconName = keyof typeof paths;

export function Icon({ name, size = 18 }: { name: IconName; size?: number }) {
  return (
    <svg
      aria-hidden="true"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d={paths[name]} />
    </svg>
  );
}

const tiles = ["#2f9e6b", "#d9507a", "#2458d6", "#d98a2b", "#7b61d6", "#1f8f9e"];

export function Avatar({ label }: { label: string }) {
  let hash = 0;
  for (const character of label) hash = (hash * 31 + character.charCodeAt(0)) >>> 0;
  return (
    <span className="avatar" aria-hidden="true" style={{ background: tiles[hash % tiles.length] }}>
      {label.trim().charAt(0).toUpperCase() || "?"}
    </span>
  );
}
