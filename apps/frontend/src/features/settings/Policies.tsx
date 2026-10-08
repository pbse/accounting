import { useEffect, useState, type ReactNode } from "react";
import { Setting, SettingList } from "@close/ui/components/SettingList";
import { workspaceRequest } from "../closes/workspace";

type AgentChat = "off" | "closes" | "firm";
export type Policy = {
  model_provider: string;
  ocr_provider?: string;
  independent_review: boolean;
  independent_review_min_amount: string;
  materiality_amount: string;
  link_passcode: boolean;
  reminder_after_days: number;
  reminder_max: number;
  agent_chat: AgentChat;
  answers_stopped: boolean;
  staff_planning: boolean;
  support_owner: string;
  hosted: boolean;
  can_manage: boolean;
  can_manage_members: boolean;
  can_read_roster: boolean;
  can_manage_links: boolean;
  can_manage_agent_chat: boolean;
  can_manage_ledger_writes: boolean;
  ledger_writes: boolean;
};

// One read of the firm's policy, shared by every row; a change reads it again.
export function usePolicy() {
  const [policy, setPolicy] = useState<Policy>();
  const [error, setError] = useState("");
  const [version, setVersion] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest("/organization/policy")
      .then((data) => live && (setPolicy(data), setError("")))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Firm policy is unavailable."));
    return () => {
      live = false;
    };
  }, [version]);
  return { policy, error, refresh: () => setVersion((v) => v + 1) };
}

// A policy change: the fields that change, with the reason the firm keeps beside them.
function useChange(onDone: (result: Record<string, unknown>) => void) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  async function run(body: Record<string, unknown>) {
    setBusy(true);
    setMessage("");
    try {
      const result = await workspaceRequest("/organization/policy", { ...body, reason });
      setReason("");
      onDone(result ?? {});
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "The change could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  const ready = reason.trim().length >= 5;
  const field = (
    <label>
      Reason for the change
      <input value={reason} maxLength={500} placeholder="Kept with the policy's history" onChange={(e) => setReason(e.target.value)} />
    </label>
  );
  return { run, busy, message, ready, field };
}

const money = (amount: string) => amount;

function IndependentReview({ policy, refresh }: { policy: Policy; refresh: () => void }) {
  const change = useChange(refresh);
  const current = policy.independent_review ? "always" : policy.independent_review_min_amount ? "large" : "off";
  const [choice, setChoice] = useState(current);
  const [amount, setAmount] = useState(policy.independent_review_min_amount);
  const status = current === "always" ? "On" : current === "large" ? `From ${money(policy.independent_review_min_amount)}` : "Off";
  const summary =
    current === "always"
      ? "Nobody may approve a draft they prepared or confirmed."
      : current === "large"
        ? `Drafts whose entries total at least ${money(policy.independent_review_min_amount)} need a reviewer who did not prepare them.`
        : "Any reviewer may approve a draft, including one they prepared.";
  const changed = choice !== current || (choice === "large" && amount.trim() !== policy.independent_review_min_amount);
  return (
    <Setting title="Independent review" summary={summary} status={status} tone={current === "off" ? "off" : "on"}>
      <p>
        With independent review on, nobody may approve a draft they prepared or confirmed, a draft without a recorded preparer cannot be approved, and posting to a client's
        live books needs an approval that meets this rule. The threshold compares a draft's proposed entries in the draft's own currency.
      </p>
      {!policy.hosted ? (
        <p className="note">Local development shares one identity, so this rule applies to hosted firms only.</p>
      ) : (
        policy.can_manage && (
          <div className="setting-change">
            <fieldset>
              <legend>Require it</legend>
              <label className="checkbox-label">
                <input type="radio" name="independent-review" checked={choice === "always"} onChange={() => setChoice("always")} /> For every draft
              </label>
              <label className="checkbox-label">
                <input type="radio" name="independent-review" checked={choice === "large"} onChange={() => setChoice("large")} /> Only for drafts whose entries total at least an amount
              </label>
              {choice === "large" && (
                <label>
                  Minimum amount
                  <input value={amount} inputMode="decimal" placeholder="1000.00" onChange={(e) => setAmount(e.target.value)} />
                </label>
              )}
              <label className="checkbox-label">
                <input type="radio" name="independent-review" checked={choice === "off"} onChange={() => setChoice("off")} /> Never
              </label>
            </fieldset>
            {change.field}
            <div className="setting-actions">
              <button
                type="button"
                className="primary"
                disabled={change.busy || !change.ready || !changed || (choice === "large" && !amount.trim())}
                onClick={() =>
                  void change.run(
                    choice === "always" ? { independent_review: true } : choice === "large" ? { independent_review: false, independent_review_min_amount: amount.trim() } : { independent_review: false, independent_review_min_amount: "" },
                  )
                }
              >
                Save
              </button>
            </div>
            {change.message && <p role="alert">{change.message}</p>}
          </div>
        )
      )}
    </Setting>
  );
}

// Writing approved entries to clients' live books is the firm's own decision: off until a firm administrator turns it
// on, with a reason. QuickBooks sandbox companies are outside it.
function LedgerWrites({ policy, refresh }: { policy: Policy; refresh: () => void }) {
  const [done, setDone] = useState("");
  const change = useChange((result) => {
    const inFlight = Number(result.writes_in_flight ?? 0);
    // Turning writing off withdraws approvals not yet acted on; writes already under way finish and are read back.
    setDone("");
    if (result.ledger_writes === false)
      setDone(`Writing is off. Live writes approved and not started need a fresh approval once it is back on.${inFlight ? ` ${inFlight} ${inFlight === 1 ? "write was" : "writes were"} already under way; ${inFlight === 1 ? "it finishes and is" : "they finish and are"} read back.` : ""}`);
    refresh();
  });
  return (
    <Setting
      title="Writing to clients' books"
      summary={policy.ledger_writes ? "Approved entries can be written to a client's live QuickBooks or Xero company." : "Nothing is written to any client's live books; approved entries are prepared for a person to book."}
      status={policy.ledger_writes ? "On" : "Off"}
      tone={policy.ledger_writes ? "on" : "off"}
    >
      <p>
        When on, an approved entry can be written to a client's live QuickBooks or Xero company by a member allowed to post, under that client's posting grant and only for
        the kinds this server allows. QuickBooks sandbox companies are not affected, and a write already made is still read back after writing is turned off.
      </p>
      {policy.can_manage_ledger_writes && (
        <div className="setting-change">
          {change.field}
          <div className="setting-actions">
            <button type="button" className="primary" disabled={change.busy || !change.ready} onClick={() => (setDone(""), void change.run({ ledger_writes: !policy.ledger_writes }))}>
              {policy.ledger_writes ? "Turn writing off" : "Turn writing on"}
            </button>
          </div>
          {done && <p role="status">{done}</p>}
          {change.message && <p role="alert">{change.message}</p>}
        </div>
      )}
    </Setting>
  );
}

function LinkPasscodes({ policy, refresh }: { policy: Policy; refresh: () => void }) {
  const change = useChange(refresh);
  return (
    <Setting
      title="Client link passcodes"
      summary={policy.link_passcode ? "Each new client request link also needs an 8-digit passcode." : "Anyone holding a client request link can read the request and reply."}
      status={policy.link_passcode ? "On" : "Off"}
      tone={policy.link_passcode ? "on" : "off"}
    >
      <p>
        With passcodes on, the person who issues a link sees its passcode once and tells the client by phone or in person; five wrong tries close the link. Links issued
        earlier keep working as they were issued.
      </p>
      {policy.can_manage_links && (
        <div className="setting-change">
          {change.field}
          <div className="setting-actions">
            <button type="button" className="primary" disabled={change.busy || !change.ready} onClick={() => void change.run({ link_passcode: !policy.link_passcode })}>
              {policy.link_passcode ? "Stop requiring passcodes" : "Require passcodes on new links"}
            </button>
          </div>
          {change.message && <p role="alert">{change.message}</p>}
        </div>
      )}
    </Setting>
  );
}

// Each reminder still needs a person's approval; the rule says when one is due and how many a request can have.
function ReminderRule({ policy, refresh }: { policy: Policy; refresh: () => void }) {
  const change = useChange(refresh);
  const [after, setAfter] = useState(policy.reminder_after_days);
  const [most, setMost] = useState(policy.reminder_max);
  const days = (n: number) => (n === 0 ? "as soon as the previous email is delivered" : `${n} ${n === 1 ? "day" : "days"} after the previous email is delivered`);
  return (
    <Setting
      title="Reminders to clients"
      summary={`A reminder is due ${days(policy.reminder_after_days)}; a request can have ${policy.reminder_max}.`}
      status={`${policy.reminder_max} ${policy.reminder_max === 1 ? "reminder" : "reminders"}`}
      tone="info"
    >
      <p>Each reminder still needs a person's approval. After the last one, or once the private link expires, contact the client another way.</p>
      {policy.can_manage_links && (
        <div className="setting-change">
          <div className="form-grid">
            <label>
              Days after delivery
              <select value={after} onChange={(e) => setAfter(Number(e.target.value))}>
                {[0, 1, 2, 3, 4, 5, 6].map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Reminders per request
              <select value={most} onChange={(e) => setMost(Number(e.target.value))}>
                {[1, 2, 3, 4, 5].map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
              </select>
            </label>
          </div>
          {change.field}
          <div className="setting-actions">
            <button
              type="button"
              className="primary"
              disabled={change.busy || !change.ready || (after === policy.reminder_after_days && most === policy.reminder_max)}
              onClick={() => void change.run({ reminder_after_days: after, reminder_max: most })}
            >
              Save
            </button>
          </div>
          {change.message && <p role="alert">{change.message}</p>}
        </div>
      )}
    </Setting>
  );
}

function Materiality({ policy, refresh }: { policy: Policy; refresh: () => void }) {
  const change = useChange(refresh);
  const [amount, setAmount] = useState("");
  return (
    <Setting
      title="Materiality"
      summary={
        policy.materiality_amount
          ? `A balance-sheet account whose Trial Balance differs from its support by less than ${money(policy.materiality_amount)} is shown as within materiality.`
          : "Every Trial Balance difference is shown as a difference to explain."
      }
      status={policy.materiality_amount ? money(policy.materiality_amount) : "Not set"}
      tone={policy.materiality_amount ? "info" : "off"}
    >
      <p>
        The comparison is per account, in the client's currency, and only while no proposed entry is pending for the account. It does not approve a draft or waive an
        entry.
      </p>
      {policy.can_manage_links && (
        <div className="setting-change">
          <label>
            Materiality amount
            <input value={amount} inputMode="decimal" placeholder={policy.materiality_amount || "500.00"} onChange={(e) => setAmount(e.target.value)} />
          </label>
          {change.field}
          <div className="setting-actions">
            <button type="button" className="primary" disabled={change.busy || !change.ready || !amount.trim()} onClick={() => void change.run({ materiality_amount: amount.trim() })}>
              Set materiality
            </button>
            {policy.materiality_amount && (
              <button type="button" disabled={change.busy || !change.ready} onClick={() => void change.run({ materiality_amount: "" })}>
                Clear
              </button>
            )}
          </div>
          {change.message && <p role="alert">{change.message}</p>}
        </div>
      )}
    </Setting>
  );
}

// The agent chat sends a member's question and the reads the model chooses to the configured provider, so it stays
// off until an administrator turns it on, for client closes or firm-wide, with a reason kept in the policy history.
const agentLevels: { value: AgentChat; label: string; detail: string }[] = [
  { value: "off", label: "Off", detail: "Questions are answered only from the assistant's fixed reads and commands and are never sent to a model. Other model work the firm has allowed (document reading, Work commands, staff plan drafts) is unchanged." },
  { value: "closes", label: "On for client closes", detail: "A question inside a client close may be investigated and answered by the model, from reads of that close made with the member's own permissions. Questions asked outside a close are never sent." },
  { value: "firm", label: "On firm-wide", detail: "As for client closes, and a question asked outside a close may be answered from the member's own lists across the clients they can open, with client names." },
];

function AgentChatSetting({ policy, refresh }: { policy: Policy; refresh: () => void }) {
  const change = useChange(refresh);
  const [choice, setChoice] = useState<AgentChat>(policy.agent_chat);
  const current = agentLevels.find((l) => l.value === policy.agent_chat) ?? agentLevels[0];
  return (
    <Setting
      title="Agent chat"
      summary={current.detail}
      status={policy.answers_stopped ? "Decision needed" : current.label}
      tone={policy.answers_stopped ? "warn" : policy.agent_chat === "off" ? "off" : "on"}
      open={policy.answers_stopped}
    >
      <p>
        When on, the question and the reads go to {policy.model_provider}, with card, bank and sign-in details removed: the close's saved status, sign-off blockers, tie-out,
        changes and exceptions (and the open draft), plus up to six reads of that close the model chooses through the assistant's read-only tools (a document's text, a draft,
        the tie-out, changes, exceptions, a search), each made with the member's own permissions, listed under the answer and kept with the attempt's record without their
        content. An answer is kept only if each read it cites was sent whole and holds the words it quotes; it links those reads and says it was written by the model. The
        model reads and proposes only: an action it proposes runs after the person confirms its card. Each read and each answer counts against the firm's daily model limit.
      </p>
      {policy.answers_stopped && (
        <p role="alert">
          Written answers stopped when this setting replaced them: this firm had turned on written answers without investigations, or firm-wide answers alone, and the agent
          now chooses its own reads, which investigations consented to. Choose a level with a reason to turn the agent on again, or apply Off to keep it off.
        </p>
      )}
      {policy.can_manage_agent_chat && (
        <div className="setting-change">
          <fieldset>
            <legend>Level</legend>
            {agentLevels.map((level) => (
              <label key={level.value} className="checkbox-label">
                <input type="radio" name="agent-chat" value={level.value} checked={choice === level.value} onChange={() => setChoice(level.value)} /> {level.label}
              </label>
            ))}
          </fieldset>
          {change.field}
          <div className="setting-actions">
            <button type="button" className="primary" disabled={change.busy || !change.ready || (choice === policy.agent_chat && !policy.answers_stopped)} onClick={() => void change.run({ agent_chat: choice })}>
              Apply
            </button>
          </div>
          {change.message && <p role="alert">{change.message}</p>}
        </div>
      )}
    </Setting>
  );
}

function StaffPlanning({ policy, refresh }: { policy: Policy; refresh: () => void }) {
  const change = useChange(refresh);
  return (
    <Setting
      title="Staff plan drafts"
      summary={policy.staff_planning ? "Platform staff may ask the model to draft a workflow plan for one of this firm's closes." : "Platform staff draft workflow plans for this firm's closes by hand."}
      status={policy.staff_planning ? "Allowed" : "Not allowed"}
      tone={policy.staff_planning ? "on" : "off"}
    >
      <p>
        Separately from the chat, platform staff may ask the model to draft a workflow plan for one of this firm's closes, sending their instruction with the close's client,
        month, sign-off state and counts of its preparation work; nothing runs before staff approve the plan.
      </p>
      {policy.can_manage_agent_chat && (
        <div className="setting-change">
          {change.field}
          <div className="setting-actions">
            <button type="button" className="primary" disabled={change.busy || !change.ready} onClick={() => void change.run({ staff_planning: !policy.staff_planning })}>
              {policy.staff_planning ? "Stop staff plan drafts" : "Allow staff plan drafts"}
            </button>
          </div>
          {change.message && <p role="alert">{change.message}</p>}
        </div>
      )}
    </Setting>
  );
}

// The firm's rules, each in one row: where it stands, what it means, and for an administrator the way to change it.
export function Policies({ policy, refresh }: { policy: Policy; refresh: () => void }) {
  const manages = policy.can_manage || policy.can_manage_links || policy.can_manage_agent_chat || policy.can_manage_ledger_writes;
  const intro: ReactNode = manages ? "Open a rule to see what it means and change it. Every change is kept with its reason." : "Open a rule to see what it means. A firm administrator changes these; every change is kept with its reason.";
  return (
    <>
      <SettingList title="Review and posting" intro={intro}>
        <IndependentReview policy={policy} refresh={refresh} />
        <LedgerWrites policy={policy} refresh={refresh} />
        <Materiality policy={policy} refresh={refresh} />
      </SettingList>
      <SettingList title="Clients and requests">
        <LinkPasscodes policy={policy} refresh={refresh} />
        <ReminderRule policy={policy} refresh={refresh} />
      </SettingList>
      <SettingList title="The agent and the model">
        <AgentChatSetting policy={policy} refresh={refresh} />
        <StaffPlanning policy={policy} refresh={refresh} />
        {policy.ocr_provider && (
          <Setting title="Reading PDFs" summary={policy.ocr_provider} status={policy.ocr_provider.split(/ reads | \(|;|:/)[0]} tone="info">
            <p>The server operator chooses how the pages of the PDFs the firm adds are read; the document worker reports it here.</p>
            <p>
              A page's own PDF text is exact and always kept; a machine reading of the page only checks it, and the page is flagged when the two differ. Scanned pages read by
              a machine are labelled to check against the original.
            </p>
          </Setting>
        )}
      </SettingList>
    </>
  );
}
