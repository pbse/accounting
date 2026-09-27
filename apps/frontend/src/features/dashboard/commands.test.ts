import assert from "node:assert/strict";
import { test } from "node:test";
import { parseCommand, runCommand, type CommandContext } from "./commands.ts";

function context(closeId: string) {
  const seen = { navigated: "", asked: "", askedClose: "" };
  const ctx: CommandContext = {
    closeId,
    navigate: (to) => void (seen.navigated = to),
    ask: (question, close) => void ((seen.asked = question), (seen.askedClose = close)),
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
  assert.deepEqual(await run("/status"), { message: "", navigated: "", asked: "Where does this close stand?", askedClose: "7" });
  assert.equal((await run("/status", "")).asked, "What needs me today?");
  assert.equal((await run("/explain Prepaid insurance")).asked, "Does Prepaid insurance tie?");
  assert.equal((await run("/explain")).asked, "What blocks sign-off?");
  assert.equal((await run("/compare Rent")).asked, "What changed in Rent from last month?");
  assert.equal((await run("/prepare")).asked, "Prepare this close");
  assert.equal((await run("/delegate")).asked, "Get this close ready for review");
  assert.equal((await run("/delegate Friday")).asked, "Get this close ready for review by Friday");
  assert.equal((await run("/stop")).asked, "Stop this close's unresolved work");
  assert.equal((await run("/request")).asked, "Draft a client request");
  assert.equal((await run("/rehearse")).navigated, "/dashboard/closes/7?tab=signoff");
  assert.equal((await run("/find Zurich invoice")).asked, "Find Zurich invoice");
  assert.equal((await run("/find")).message, "Type what to find after /find.");
  assert.equal((await run("/review")).navigated, "/dashboard/closes/9/workpapers/44");
  assert.equal((await run("/prepare", "")).message, "Choose a client close first.");
  assert.equal((await run("/ask")).message, "Type your question after /ask.");
});
