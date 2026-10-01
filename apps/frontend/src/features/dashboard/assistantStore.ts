import { useSyncExternalStore } from "react";
import { workspaceRequest } from "../closes/workspace";

// A question asked from outside the panel (the command palette) waits for the conversation of its close.
let pending: { question: string; closeId: string } | null = null;
const listeners = new Set<() => void>();

export function askAssistant(question: string, closeId: string) {
  pending = { question, closeId };
  listeners.forEach((l) => l());
}

// takePending hands the waiting question to the conversation of its close, once.
export function takePending(closeId: string) {
  if (pending?.closeId !== closeId) return "";
  const question = pending.question;
  pending = null;
  return question;
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function usePending() {
  return useSyncExternalStore(subscribe, () => pending);
}

// Opening the panel from elsewhere (the sidebar) is counted, so every click opens and focuses it.
let openRequests = 0;

export function openAssistant() {
  openRequests += 1;
  listeners.forEach((l) => l());
}

export function useOpenRequests() {
  return useSyncExternalStore(subscribe, () => openRequests);
}

// nextReview is the first draft in the review queue, the target of /review.
export async function nextReview(): Promise<{ close_id: string; workpaper_id: string } | undefined> {
  const queue = await workspaceRequest("/review-queue");
  return (queue.items as { close_id: string; workpaper_id: string }[])[0];
}
