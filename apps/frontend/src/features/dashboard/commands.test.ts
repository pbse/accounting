import assert from "node:assert/strict";
import { test } from "node:test";
import { parseCommand, runCommand, type CommandContext } from "./commands.ts";

function context(closeId: string) {
  const seen = { navigated: "", asked: "", askedClose: "" };
  const ctx: CommandContext = {
    closeId,
    navigate: (to) => void (seen.navigated = to),
    ask: (what, close) => void ((seen.asked = "text" in what ? what.text : `/${what.command}${what.argument ? " " + what.argument : ""}`), (seen.askedClose = close)),
    nextReview: async () => ({ close_id: "9", workpaper_id: "44" }),
  };
  return { seen, ctx };
}

test("a command line names a command and its argument, with or without the slash", () => {
  assert.equal(parseCommand("/explain Prepaid insurance").command?.name, "explain");
  assert.equal(parseCommand("explain Prepaid insurance").argument, "Prepaid insurance");
  assert.equal(parseCommand("/nothing").command, undefined);
});

test("commands ask the assistant or open the screen that does the work", async () => {
  const run = async (line: string, closeId = "7") => {
    const { command, argument } = parseCommand(line);
    const { seen, ctx } = context(closeId);
    const message = await runCommand(command!, argument, ctx);
    return { message, ...seen };
  };
  // A command is sent as itself: the server runs its fixed read or card without a model.
  assert.deepEqual(await run("/status"), { message: "", navigated: "", asked: "/status", askedClose: "7" });
  assert.equal((await run("/status", "")).asked, "/status");
  assert.equal((await run("/explain Prepaid insurance")).asked, "/explain Prepaid insurance");
  assert.equal((await run("/explain")).asked, "/explain");
  assert.equal((await run("/compare Rent")).asked, "/compare Rent");
  assert.equal((await run("/prepare")).asked, "/prepare");
  assert.equal((await run("/delegate")).asked, "/delegate");
  assert.equal((await run("/delegate Friday")).asked, "/delegate Friday");
  assert.equal((await run("/stop")).asked, "/stop");
  assert.equal((await run("/request")).asked, "/request");
  assert.equal((await run("/rehearse")).navigated, "/dashboard/closes/7?tab=signoff");
  assert.equal((await run("/find Zurich invoice")).asked, "/find Zurich invoice");
  assert.equal((await run("/ask why is cash low")).asked, "why is cash low");
  assert.equal((await run("/find")).message, "Type what to find after /find.");
  assert.equal((await run("/review")).navigated, "/dashboard/closes/9/workpapers/44");
  assert.equal((await run("/prepare", "")).message, "Choose a client close first.");
  assert.equal((await run("/ask")).message, "Type your question after /ask.");
});
