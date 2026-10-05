// A note from the public contact page, as one plain-text email to our inbox (docs/marketing-site.md). The API has
// already kept the note; this only forwards it. Header values never carry a line break, and every word a visitor
// wrote is encoded, so nothing they type can add a header or change who receives the message.
export type ContactNote = { id: number; name: string; email: string; firm: string; note: string };

const encoder = new TextEncoder();
function base64(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}
const oneLine = (value: string) => value.replace(/[\u0000-\u001f\u007f\u2028\u2029]+/g, " ").trim();

// RFC 2047 encoded words of at most 45 bytes each, split between characters and folded onto separate lines.
export function encodedWords(value: string): string {
  const words: string[] = [];
  let chunk = "";
  for (const character of oneLine(value)) {
    if (encoder.encode(chunk + character).length > 45) {
      words.push(chunk);
      chunk = "";
    }
    chunk += character;
  }
  if (chunk) words.push(chunk);
  return words.map((word) => `=?UTF-8?B?${base64(encoder.encode(word))}?=`).join("\r\n ") || '""';
}

const plainAddress = /^[^\s<>()@,;:\\"[\]]+@[^\s<>()@,;:\\"[\]]+\.[^\s<>()@,;:\\"[\]]+$/;

const DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
// "Mon, 05 Oct 2026 04:19:12 +0000"
export function messageDate(at: Date): string {
  const two = (n: number) => String(n).padStart(2, "0");
  return `${DAYS[at.getUTCDay()]}, ${two(at.getUTCDate())} ${MONTHS[at.getUTCMonth()]} ${at.getUTCFullYear()} ${two(at.getUTCHours())}:${two(at.getUTCMinutes())}:${two(at.getUTCSeconds())} +0000`;
}

export function contactMessage(note: ContactNote, from: string, to: string, at = new Date(), unique = crypto.randomUUID()): string {
  const domain = from.split("@")[1] ?? "localhost";
  const headers = [
    `From: Krith website <${from}>`,
    `To: <${to}>`,
    ...(plainAddress.test(note.email) ? [`Reply-To: ${encodedWords(note.name)} <${note.email}>`] : []),
    `Subject: ${encodedWords(`Contact: ${note.name}, ${note.firm}`)}`,
    `Date: ${messageDate(at)}`,
    `Message-ID: <contact-${Math.trunc(note.id)}-${unique}@${domain}>`,
    "MIME-Version: 1.0",
    "Content-Type: text/plain; charset=utf-8",
    "Content-Transfer-Encoding: base64",
  ];
  const body = [
    `Name: ${oneLine(note.name)}`,
    `Email: ${oneLine(note.email)}`,
    `Firm: ${oneLine(note.firm)}`,
    "",
    note.note.trim() || "(No note)",
    "",
    `Sent through the contact page. Kept as contact note ${Math.trunc(note.id)}; reply to this email to answer.`,
  ].join("\r\n");
  const encoded = base64(encoder.encode(body)).replace(/.{1,76}/g, "$&\r\n");
  return `${headers.join("\r\n")}\r\n\r\n${encoded}`;
}

// The kept note the API returned, or null when there is nothing to forward (a trap-field note, or an unexpected reply).
export function noteToForward(reply: unknown): ContactNote | null {
  if (typeof reply !== "object" || reply === null) return null;
  const r = reply as Record<string, unknown>;
  if (r.forward !== true || typeof r.id !== "number" || [r.name, r.email, r.firm, r.note].some((v) => typeof v !== "string")) return null;
  return { id: r.id, name: r.name as string, email: r.email as string, firm: r.firm as string, note: r.note as string };
}
