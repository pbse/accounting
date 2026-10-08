// close: the command needs a close. firm: it answers across closes, so from a close it goes to the firm conversation.
export type Command = { name: string; description: string; close?: boolean; firm?: boolean; argument?: string };

export const commands: Command[] = [
  { name: "ask", description: "Ask the assistant about this close or your work", argument: "question" },
  { name: "status", description: "Where a close stands, or what needs you today" },
  { name: "queue", description: "Drafts ready for review, across your closes", firm: true },
  { name: "blocked", description: "Blocked work, across your closes", firm: true },
  { name: "delegated", description: "The closes handed to the agent and how they are doing", firm: true },
  { name: "explain", description: "Explain a balance that doesn't tie, or what blocks sign-off", close: true, argument: "account" },
  { name: "compare", description: "Compare with last month, optionally for one account", close: true, argument: "account" },
  { name: "find", description: "Find documents, drafts, transactions, procedures or review decisions", argument: "words" },
  { name: "delegate", description: "Hand a close to the agent until it's ready for review", close: true, argument: "day" },
  { name: "prepare", description: "Preview and start preparing a close", close: true },
  { name: "request", description: "Draft a consolidated client request", close: true },
  { name: "review", description: "Open the next draft ready for review" },
  { name: "rehearse", description: "Preview the combined effect of saved proposals", close: true },
  { name: "export", description: "Download the review package or signed binder", close: true },
  { name: "stop", description: "Preview and stop unresolved preparation", close: true },
];

export function parseCommand(line: string) {
  const [head = "", ...rest] = line.trim().replace(/^\//, "").split(/\s+/);
  return { command: commands.find((c) => c.name === head.toLowerCase()), argument: rest.join(" ").trim() };
}

// A command the server runs without a model, as {command, argument}; /ask sends free text.
export type Ask = { text: string } | { command: string; argument: string };

export type CommandContext = {
  closeId: string;
  navigate: (to: string) => void;
  ask: (what: Ask, closeId: string) => void;
  nextReview: () => Promise<{ close_id: string; workpaper_id: string } | undefined>;
};

// runCommand returns a message to show when it cannot act, or "" once it has.
export async function runCommand(command: Command, argument: string, ctx: CommandContext): Promise<string> {
  const close = ctx.closeId;
  const base = `/dashboard/closes/${close}`;
  if (command.close && !close) return "Choose a client close first.";
  switch (command.name) {
    case "ask":
      if (!argument) return "Type your question after /ask.";
      ctx.ask({ text: argument }, close);
      return "";
    case "find":
      if (!argument) return "Type what to find after /find.";
      ctx.ask({ command: "find", argument }, close);
      return "";
    case "queue":
    case "blocked":
    case "delegated":
      ctx.ask({ command: command.name, argument }, "");
      return "";
    case "status":
    case "explain":
    case "compare":
    case "delegate":
    case "prepare":
    case "stop":
    case "request":
      ctx.ask({ command: command.name, argument }, close);
      return "";
    case "rehearse":
    case "export":
      ctx.navigate(`${base}?tab=signoff`);
      return "";
    case "review": {
      const next = await ctx.nextReview();
      if (!next) return "No draft is ready for review.";
      ctx.navigate(`/dashboard/closes/${next.close_id}/workpapers/${next.workpaper_id}`);
      return "";
    }
  }
  return "";
}
