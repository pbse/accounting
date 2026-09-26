// citedLines gives the extracted-text lines a citation points at: "line(s) N[-M]" counts from the top, and a PDF's
// "page P, line L" counts from that page's marker line.
export function citedLines(location: string, text: string) {
  const match = /lines?\s+(\d+)(?:\s*(?:-|–|to)\s*(\d+))?/i.exec(location);
  if (!match) return undefined;
  let first = Number(match[1]);
  let last = Number(match[2] ?? first);
  const page = /page\s+(\d+)/i.exec(location);
  if (page) {
    const marker = text.split("\n").findIndex((line) => line.startsWith(`--- Page ${page[1]} ·`));
    if (marker < 0) return undefined;
    first += marker + 1;
    last += marker + 1;
  }
  return [first, last] as const;
}

export const citedPage = (location: string) => Number(/page\s+(\d+)/i.exec(location)?.[1] ?? 1);
