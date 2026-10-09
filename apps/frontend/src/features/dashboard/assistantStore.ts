import { useSyncExternalStore } from "react";
import { workspaceRequest } from "../closes/workspace";
import type { Ask } from "./commands";

// Starter questions: each is a command the panel's fixed reads answer without a model, shown by its question.
export const starters = {
  firm: ["What needs me today?", "What is ready for review?", "What is blocked?"],
  close: ["Where does this close stand?", "What blocks sign-off?", "What changed from last month?"],
  // An executive's questions go to the agent as they are: there are no commands for an executive.
  executive: ["How is my business doing?", "What does my latest report say?", "What is my cash position for the next 13 weeks?"],
};
export const starterCommands: Record<string, Ask> = {
  "What needs me today?": { command: "status", argument: "" },
  "What is ready for review?": { command: "queue", argument: "" },
  "What is blocked?": { command: "blocked", argument: "" },
  "Where does this close stand?": { command: "status", argument: "" },
  "What blocks sign-off?": { command: "explain", argument: "" },
  "What changed from last month?": { command: "compare", argument: "" },
};

// A question asked from outside the panel (the command palette, a close's page) waits for the conversation of its close.
let pending: { what: Ask; closeId: string } | null = null;
const listeners = new Set<() => void>();

export function askAssistant(what: Ask | string, closeId: string) {
  pending = { what: typeof what === "string" ? { text: what } : what, closeId };
  listeners.forEach((l) => l());
}

// takePending hands the waiting question to the conversation of its close, once.
export function takePending(closeId: string): Ask | null {
  if (pending?.closeId !== closeId) return null;
  const what = pending.what;
  pending = null;
  return what;
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function usePending() {
  // A server render, and the browser's hydration of it, see nothing pending.
  return useSyncExternalStore(subscribe, () => pending, () => null);
}

// Opening the panel from elsewhere (the sidebar) is counted, so every click opens and focuses it.
let openRequests = 0;

export function openAssistant() {
  openRequests += 1;
  listeners.forEach((l) => l());
}

export function useOpenRequests() {
  return useSyncExternalStore(subscribe, () => openRequests, () => 0);
}

// A chat (session) with the agent, as the server lists it.
export type ChatSession = { id: string; title: string; close_id: string; close: string; last_at: string; messages?: number };

// Opening a chat from outside the panel (the rail's recent chats, the panel's own list): "new" starts a fresh chat in
// the scope of closeId ("" firm-wide), "all" shows the list, and an id opens that chat. The panel of that scope takes
// it; the layout first moves to the close's page when the chat is a close's.
let requested: { session: string; closeId: string; n: number } | null = null;
let requests = 0;

export function openSession(session: string, closeId: string) {
  requests += 1;
  requested = { session, closeId, n: requests };
  openRequests += 1;
  listeners.forEach((l) => l());
}

export function useSessionRequest() {
  return useSyncExternalStore(subscribe, () => requested, () => null);
}

// Every list of chats (the rail's, the panel's) reloads when a chat is made, renamed or deleted.
let sessionsVersion = 0;

export function bumpSessions() {
  sessionsVersion += 1;
  listeners.forEach((l) => l());
}

export function useSessionsVersion() {
  return useSyncExternalStore(subscribe, () => sessionsVersion, () => 0);
}

export async function loadSessions(limit = 50, offset = 0, q = ""): Promise<{ sessions: ChatSession[]; hasMore: boolean }> {
  const data = await workspaceRequest(`/chat/sessions?limit=${limit}&offset=${offset}${q ? `&q=${encodeURIComponent(q)}` : ""}`);
  return { sessions: (data.sessions ?? []) as ChatSession[], hasMore: !!data.has_more };
}

// nextReview is the first draft in the review queue, the target of /review.
export async function nextReview(): Promise<{ close_id: string; workpaper_id: string } | undefined> {
  const queue = await workspaceRequest("/review-queue");
  return (queue.items as { close_id: string; workpaper_id: string }[])[0];
}

