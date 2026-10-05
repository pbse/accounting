import { test } from "node:test";
import assert from "node:assert/strict";
import { contactMessage, encodedWords, messageDate, noteToForward } from "./contact.ts";

const decodeWords = (value: string) =>
  value
    .split("\r\n ")
    .map((word) => Buffer.from(/^=\?UTF-8\?B\?(.*)\?=$/.exec(word)![1], "base64").toString("utf8"))
    .join("");
const parts = (raw: string) => {
  const [head, body] = raw.split("\r\n\r\n");
  const headers = head.split(/\r\n(?![ \t])/);
  return { headers, body: Buffer.from(body.replace(/\r\n/g, ""), "base64").toString("utf8") };
};

test("a visitor's line breaks cannot add headers or recipients", () => {
  const raw = contactMessage({ id: 7, name: "Eve\r\nBcc: someone@else.example", email: "eve@firm.example", firm: "Firm\nCc: x@y.example", note: "Hi" }, "website@getkrith.com", "inbox@owner.example", new Date(Date.UTC(2026, 9, 5, 4, 19, 12)), "u1");
  const { headers } = parts(raw);
  assert.deepEqual(headers.map((h) => h.split(":")[0]), ["From", "To", "Reply-To", "Subject", "Date", "Message-ID", "MIME-Version", "Content-Type", "Content-Transfer-Encoding"]);
  assert.ok(!/^(Bcc|Cc):/im.test(raw.split("\r\n\r\n")[0]));
  assert.equal(headers[1], "To: <inbox@owner.example>");
  assert.equal(decodeWords(headers[3].slice("Subject: ".length)), "Contact: Eve Bcc: someone@else.example, Firm Cc: x@y.example");
  assert.equal(headers[4], "Date: Mon, 05 Oct 2026 04:19:12 +0000");
  assert.equal(headers[5], "Message-ID: <contact-7-u1@getkrith.com>");
});

test("the body carries the note, and replying answers the visitor", () => {
  const raw = contactMessage({ id: 12, name: "Ana Lima", email: "ana@firm.example", firm: "Lima & Co", note: "40 clients on QuickBooks Online.\nMost closes take a week. Café ✓" }, "website@getkrith.com", "inbox@owner.example");
  const { headers, body } = parts(raw);
  assert.match(headers[2], /^Reply-To: =\?UTF-8\?B\?.+\?= <ana@firm\.example>$/);
  assert.ok(body.startsWith("Name: Ana Lima\r\nEmail: ana@firm.example\r\nFirm: Lima & Co\r\n\r\n40 clients on QuickBooks Online.\nMost closes take a week. Café ✓"));
  assert.ok(body.includes("contact note 12"));
  for (const line of raw.split("\r\n")) assert.ok(line.length <= 998, "every line stays within the limit");
});

test("an address that is not plain gets no Reply-To", () => {
  const raw = contactMessage({ id: 1, name: "Ana", email: "Ana <ana@firm.example>", firm: "F", note: "" }, "website@getkrith.com", "inbox@owner.example");
  assert.ok(!raw.includes("Reply-To:"));
  assert.ok(parts(raw).body.includes("(No note)"));
});

test("long and non-ASCII header text is split between characters", () => {
  const value = "Ünïcödé ".repeat(12) + "✓";
  const encoded = encodedWords(value);
  for (const word of encoded.split("\r\n ")) assert.ok(word.length <= 75, word);
  assert.equal(decodeWords(encoded), value.trim());
  assert.equal(messageDate(new Date(Date.UTC(2026, 0, 2, 3, 4, 5))), "Fri, 02 Jan 2026 03:04:05 +0000");
});

test("only a kept note is forwarded", () => {
  assert.deepEqual(noteToForward({ forward: true, id: 3, name: "A", email: "a@b.example", firm: "F", note: "" }), { id: 3, name: "A", email: "a@b.example", firm: "F", note: "" });
  assert.equal(noteToForward({ forward: false }), null);
  assert.equal(noteToForward({ forward: true, id: "3", name: "A", email: "a@b.example", firm: "F", note: "" }), null);
  assert.equal(noteToForward(null), null);
});
