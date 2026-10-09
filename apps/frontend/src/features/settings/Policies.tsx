import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Dialog, DialogActions } from "@close/ui/components/Dialog";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import { SettingCard, SettingGrid } from "@close/ui/components/SettingCard";
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

// One read of the firm's policy, shared by every card; a change reads it again.
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

type Change = { body: Record<string, unknown>; reason: string };

// A card's change happens in a dialog: the explanation, the controls, the reason the firm keeps, and Save. The
// dialog closes on success and the card says so; a refusal shows in the dialog with the draft intact.
function Edit({
  title,
  description,
  explanation,
  open,
  onClose,
  onSave,
  onReset,
  ready,
  readOnly = false,
  saveLabel = "Save",
  children,
}: {
  title: string;
  description?: ReactNode;
  explanation: ReactNode;
  open: boolean;
  onClose: () => void;
  onSave?: (reason: string) => Promise<void>;
  onReset?: () => void;
  ready?: boolean;
  readOnly?: boolean;
  saveLabel?: string;
  children?: ReactNode;
}) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  // Leaving without saving discards the draft: the next opening starts from the rule as it stands.
  const close = () => {
    setReason("");
    setMessage("");
    onReset?.();
    onClose();
  };
  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setMessage("");
    try {
      await onSave?.(reason.trim());
      setReason("");
      onClose();
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "The change could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  if (readOnly)
    return (
      <Dialog open={open} onClose={close} title={title} description={description}>
        {explanation}
        <DialogActions>
          <button type="button" onClick={close}>
            Close
          </button>
        </DialogActions>
      </Dialog>
    );
  return (
    <Dialog open={open} onClose={close} title={title} description={description}>
      <form onSubmit={(e) => void submit(e)}>
        <fieldset disabled={busy}>
          {children}
          {explanation}
          <label>
            Reason for the change
            <input value={reason} maxLength={500} placeholder="Kept with the policy's history" onChange={(e) => setReason(e.target.value)} />
          </label>
          {message && <p role="alert">{message}</p>}
          <DialogActions>
            <button type="button" onClick={close}>
              Cancel
            </button>
            <button className="primary" disabled={busy || !ready || reason.trim().length < 5}>
              {saveLabel}
            </button>
          </DialogActions>
        </fieldset>
      </form>
    </Dialog>
  );
}

// What a card says after its change: kept until the next one.
function useSaved() {
  const [saved, setSaved] = useState("");
  return { note: saved ? <p role="status">{saved}</p> : null, setSaved };
}

const change = (body: Record<string, unknown>, reason: string): Promise<Record<string, unknown>> => workspaceRequest("/organization/policy", { ...body, reason });

const editButton = (label: string, onClick: () => void) => (
  <button type="button" onClick={onClick}>
    {label}
  </button>
);
// Those who cannot change a rule still open what it means, one interaction away.
const action = (allowed: boolean, label: string, onClick: () => void) => editButton(allowed ? label : "Details", onClick);

function IndependentReview({ policy, refresh }: { policy: Policy; refresh: () => void }) {
  const [open, setOpen] = useState(false);
  const { note, setSaved } = useSaved();
  const current = policy.independent_review ? "always" : policy.independent_review_min_amount ? "large" : "off";
  const [choice, setChoice] = useState(current);
  const [amount, setAmount] = useState(policy.independent_review_min_amount);
  // The policy read again after any change, by anyone, is the draft's new starting point.
  useEffect(() => {
    setChoice(current);
    setAmount(policy.independent_review_min_amount);
  }, [current, policy.independent_review_min_amount]);
  const changed = choice !== current || (choice === "large" && amount.trim() !== policy.independent_review_min_amount);
  const body: Change["body"] = choice === "always" ? { independent_review: true } : choice === "large" ? { independent_review: false, independent_review_min_amount: amount.trim() } : { independent_review: false, independent_review_min_amount: "" };
  return (
    <SettingCard
      icon="review"
      tone={current === "off" ? "gray" : "green"}
      title="Independent review"
      status={current === "always" ? "Every draft" : current === "large" ? `From ${policy.independent_review_min_amount}` : "Off"}
      statusTone={current === "off" ? "off" : "on"}
      summary={
        current === "always"
          ? "Nobody may approve a draft they prepared or confirmed."
          : current === "large"
            ? `Drafts whose entries total at least ${policy.independent_review_min_amount} need a reviewer who did not prepare them.`
            : "Any reviewer may approve a draft, including one they prepared."
      }
      action={action(policy.hosted && policy.can_manage, "Edit", () => setOpen(true))}
    >
      {note ?? (!policy.hosted && <p>Hosted firms only.</p>)}
      <Edit
        title="Independent review"
        description="Who may approve a draft, and whether that depends on its size."
        explanation={
          <p>
            With independent review on, nobody may approve a draft they prepared or confirmed, a draft without a recorded preparer cannot be approved, and posting to a
            client's live books needs an approval that meets this rule. The threshold compares a draft's proposed entries in the draft's own currency.
          </p>
        }
        open={open}
        onClose={() => setOpen(false)}
        onReset={() => (setChoice(current), setAmount(policy.independent_review_min_amount))}
        readOnly={!(policy.hosted && policy.can_manage)}
        ready={changed && (choice !== "large" || !!amount.trim())}
        onSave={async (reason) => {
          await change(body, reason);
          setSaved("Saved.");
          refresh();
        }}
      >
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
      </Edit>
    </SettingCard>
  );
}

// Writing approved entries to clients' live books is the firm's own decision: off until a firm administrator turns it
// on, with a reason. QuickBooks sandbox companies are outside it.
function LedgerWrites({ policy, refresh }: { policy: Policy; refresh: () => void }) {
  const [open, setOpen] = useState(false);
  const { note, setSaved } = useSaved();
  return (
    <SettingCard
      icon="book"
      tone={policy.ledger_writes ? "green" : "gray"}
      title="Writing to clients' books"
      status={policy.ledger_writes ? "On" : "Off"}
      statusTone={policy.ledger_writes ? "on" : "off"}
      summary={policy.ledger_writes ? "Approved entries can be written to a client's live QuickBooks or Xero company." : "Nothing is written to any client's live books; approved entries are prepared for a person to book."}
      action={action(policy.can_manage_ledger_writes, policy.ledger_writes ? "Turn off" : "Turn on", () => setOpen(true))}
    >
      {note}
      <Edit
        title={policy.ledger_writes ? "Turn writing off" : "Turn writing on"}
        description="Whether approved entries may be written to clients' live books."
        explanation={
          <p>
            When on, an approved entry can be written to a client's live QuickBooks or Xero company by a member allowed to post, under that client's posting grant and only
            for the kinds this server allows. QuickBooks sandbox companies are not affected, and a write already made is still read back after writing is turned off.
          </p>
        }
        open={open}
        onClose={() => setOpen(false)}
        readOnly={!policy.can_manage_ledger_writes}
        ready
        saveLabel={policy.ledger_writes ? "Turn writing off" : "Turn writing on"}
        onSave={async (reason) => {
          const result = await change({ ledger_writes: !policy.ledger_writes }, reason);
          const inFlight = Number(result.writes_in_flight ?? 0);
          // Turning writing off withdraws approvals not yet acted on; writes already under way finish and are read back.
          setSaved(
            result.ledger_writes === false
              ? `Writing is off. Live writes approved and not started need a fresh approval once it is back on.${inFlight ? ` ${inFlight} ${inFlight === 1 ? "write was" : "writes were"} already under way; ${inFlight === 1 ? "it finishes and is" : "they finish and are"} read back.` : ""}`
              : "Writing is on.",
          );
          refresh();
        }}
      />
    </SettingCard>
  );
}

function Materiality({ policy, refresh }: { policy: Policy; refresh: () => void }) {
  const [open, setOpen] = useState(false);
  const { note, setSaved } = useSaved();
  const [amount, setAmount] = useState("");
  const [clear, setClear] = useState(false);
  return (
    <SettingCard
      icon="measure"
      tone={policy.materiality_amount ? "blue" : "gray"}
      title="Materiality"
      status={policy.materiality_amount || "Not set"}
      statusTone={policy.materiality_amount ? "info" : "off"}
      summary={policy.materiality_amount ? `A balance-sheet account whose Trial Balance differs from its support by less than ${policy.materiality_amount} is shown as within materiality.` : "Every Trial Balance difference is shown as a difference to explain."}
      action={action(policy.can_manage_links, "Edit", () => (setAmount(""), setClear(false), setOpen(true)))}
    >
      {note}
      <Edit
        title="Materiality"
        description="The difference per account, in the client's currency, below which a balance-sheet account is shown as within materiality."
        explanation={<p>The comparison is per account, only while no proposed entry is pending for the account. It does not approve a draft or waive an entry.</p>}
        open={open}
        onClose={() => setOpen(false)}
        onReset={() => (setAmount(""), setClear(false))}
        readOnly={!policy.can_manage_links}
        ready={clear ? !!policy.materiality_amount : !!amount.trim()}
        onSave={async (reason) => {
          await change({ materiality_amount: clear ? "" : amount.trim() }, reason);
          setSaved("Saved.");
          refresh();
        }}
      >
        <label>
          Materiality amount
          <input value={amount} inputMode="decimal" disabled={clear} placeholder={policy.materiality_amount || "500.00"} onChange={(e) => setAmount(e.target.value)} />
        </label>
        {policy.materiality_amount && (
          <label className="checkbox-label">
            <input type="checkbox" checked={clear} onChange={(e) => setClear(e.target.checked)} /> Clear materiality: show every difference to explain
          </label>
        )}
      </Edit>
    </SettingCard>
  );
}

function LinkPasscodes({ policy, refresh }: { policy: Policy; refresh: () => void }) {
  const [open, setOpen] = useState(false);
  const { note, setSaved } = useSaved();
  return (
    <SettingCard
      icon="shield"
      tone={policy.link_passcode ? "green" : "gray"}
      title="Client link passcodes"
      status={policy.link_passcode ? "Required" : "Off"}
      statusTone={policy.link_passcode ? "on" : "off"}
      summary={policy.link_passcode ? "Each new client request link also needs an 8-digit passcode." : "Anyone holding a client request link can read the request and reply."}
      action={action(policy.can_manage_links, policy.link_passcode ? "Stop requiring" : "Require", () => setOpen(true))}
    >
      {note}
      <Edit
        title={policy.link_passcode ? "Stop requiring passcodes" : "Require passcodes on new links"}
        description="Whether a client needs a passcode as well as the link."
        explanation={
          <p>
            With passcodes on, the person who issues a link sees its passcode once and tells the client by phone or in person; five wrong tries close the link. Links issued
            earlier keep working as they were issued.
          </p>
        }
        open={open}
        onClose={() => setOpen(false)}
        readOnly={!policy.can_manage_links}
        ready
        saveLabel={policy.link_passcode ? "Stop requiring passcodes" : "Require passcodes"}
        onSave={async (reason) => {
          await change({ link_passcode: !policy.link_passcode }, reason);
          setSaved("Saved.");
          refresh();
        }}
      />
    </SettingCard>
  );
}

// Each reminder still needs a person's approval; the rule says when one is due and how many a request can have.
function ReminderRule({ policy, refresh }: { policy: Policy; refresh: () => void }) {
  const [open, setOpen] = useState(false);
  const { note, setSaved } = useSaved();
  const [after, setAfter] = useState(policy.reminder_after_days);
  const [most, setMost] = useState(policy.reminder_max);
  useEffect(() => {
    setAfter(policy.reminder_after_days);
    setMost(policy.reminder_max);
  }, [policy.reminder_after_days, policy.reminder_max]);
  const days = (n: number) => (n === 0 ? "as soon as the previous email is delivered" : `${n} ${n === 1 ? "day" : "days"} after the previous email is delivered`);
  return (
    <SettingCard
      icon="mail"
      tone="blue"
      title="Reminders to clients"
      status={`${policy.reminder_max} ${policy.reminder_max === 1 ? "reminder" : "reminders"}`}
      statusTone="info"
      summary={`A reminder is due ${days(policy.reminder_after_days)}; a request can have ${policy.reminder_max}.`}
      action={action(policy.can_manage_links, "Edit", () => setOpen(true))}
    >
      {note}
      <Edit
        title="Reminders to clients"
        description="When a reminder is due and how many a request can have. Each still needs a person's approval."
        explanation={<p>After the last reminder, or once the private link expires, contact the client another way.</p>}
        open={open}
        onClose={() => setOpen(false)}
        onReset={() => (setAfter(policy.reminder_after_days), setMost(policy.reminder_max))}
        readOnly={!policy.can_manage_links}
        ready={after !== policy.reminder_after_days || most !== policy.reminder_max}
        onSave={async (reason) => {
          await change({ reminder_after_days: after, reminder_max: most }, reason);
          setSaved("Saved.");
          refresh();
        }}
      >
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
      </Edit>
    </SettingCard>
  );
}

// The agent chat sends a member's question and the reads the model chooses to the configured provider, so it stays
// off until an administrator turns it on, for client closes or firm-wide, with a reason kept in the policy history.
const agentLevels: { value: AgentChat; label: string; detail: string }[] = [
  { value: "off", label: "Off", detail: "Questions are answered only from the assistant's fixed reads and commands and are never sent to a model. Other model work the firm has allowed (document reading, Work commands, staff plan drafts) is unchanged." },
  { value: "closes", label: "On for client closes", detail: "A question inside a client close may be investigated and answered by the model, from reads of that close made with the member's own permissions. Questions asked outside a close are never sent." },
  { value: "firm", label: "On firm-wide", detail: "As for client closes, and a question asked outside a close may be answered from the member's own lists across the clients they can open, with client names. A client's executive may then ask about their published reports, cash plan and budget." },
];

function AgentChatSetting({ policy, refresh }: { policy: Policy; refresh: () => void }) {
  const [open, setOpen] = useState(false);
  const { note, setSaved } = useSaved();
  const [choice, setChoice] = useState<AgentChat>(policy.agent_chat);
  useEffect(() => setChoice(policy.agent_chat), [policy.agent_chat]);
  const current = agentLevels.find((l) => l.value === policy.agent_chat) ?? agentLevels[0];
  return (
    <SettingCard
      icon="agent"
      tone={policy.answers_stopped ? "orange" : policy.agent_chat === "off" ? "gray" : "green"}
      title="Agent chat"
      status={policy.answers_stopped ? "Decision needed" : current.label}
      statusTone={policy.answers_stopped ? "warn" : policy.agent_chat === "off" ? "off" : "on"}
      summary={policy.answers_stopped ? "Written answers stopped when this setting replaced them. Choose a level, or Off, with a reason." : current.detail}
      action={action(policy.can_manage_agent_chat, "Edit", () => setOpen(true))}
    >
      {note}
      <Edit
        title="Agent chat"
        description={`Whether a member's question, and the reads the model chooses, may go to ${policy.model_provider}.`}
        explanation={
          <>
            {policy.can_manage_agent_chat || (
              <ul>
                {agentLevels.map((level) => (
                  <li key={level.value}>
                    <strong>{level.label}.</strong> {level.detail}
                  </li>
                ))}
              </ul>
            )}
            <p>
              When on, the question and the reads go to {policy.model_provider}, with card, bank and sign-in details removed: the close's saved status, sign-off blockers,
              tie-out, changes and exceptions (and the open draft), plus up to six reads of that close the model chooses through the assistant's read-only tools (a document's
              text, a draft, the tie-out, changes, exceptions, a search), each made with the member's own permissions, listed under the answer and kept with the attempt's
              record without their content. An answer is kept only if each read it cites was sent whole and holds the words it quotes; it links those reads and says it was
              written by the model. The model reads and proposes only: an action it proposes runs after the person confirms its card. Each read and each answer counts against
              the firm's daily model limit.
            </p>
          </>
        }
        open={open}
        onClose={() => setOpen(false)}
        onReset={() => setChoice(policy.agent_chat)}
        readOnly={!policy.can_manage_agent_chat}
        ready={choice !== policy.agent_chat || policy.answers_stopped}
        saveLabel="Apply"
        onSave={async (reason) => {
          await change({ agent_chat: choice }, reason);
          setSaved("Saved.");
          refresh();
        }}
      >
        {policy.answers_stopped && (
          <p>
            Written answers stopped when this setting replaced them: this firm had turned on written answers without investigations, or firm-wide answers alone, and the
            agent now chooses its own reads, which investigations consented to. Choose a level with a reason to turn the agent on again, or apply Off to keep it off.
          </p>
        )}
        <fieldset>
          <legend>Level</legend>
          {agentLevels.map((level) => (
            <label key={level.value} className="checkbox-label">
              <input type="radio" name="agent-chat" value={level.value} checked={choice === level.value} onChange={() => setChoice(level.value)} />
              <span>
                <strong>{level.label}.</strong> {level.detail}
              </span>
            </label>
          ))}
        </fieldset>
      </Edit>
    </SettingCard>
  );
}

function StaffPlanning({ policy, refresh }: { policy: Policy; refresh: () => void }) {
  const [open, setOpen] = useState(false);
  const { note, setSaved } = useSaved();
  return (
    <SettingCard
      icon="run"
      tone={policy.staff_planning ? "green" : "gray"}
      title="Staff plan drafts"
      status={policy.staff_planning ? "Allowed" : "Not allowed"}
      statusTone={policy.staff_planning ? "on" : "off"}
      summary={policy.staff_planning ? "Platform staff may ask the model to draft a workflow plan for one of this firm's closes." : "Platform staff draft workflow plans for this firm's closes by hand."}
      action={action(policy.can_manage_agent_chat, policy.staff_planning ? "Stop" : "Allow", () => setOpen(true))}
    >
      {note}
      <Edit
        title={policy.staff_planning ? "Stop staff plan drafts" : "Allow staff plan drafts"}
        description="Whether platform staff may ask the model to draft a workflow plan for one of this firm's closes."
        explanation={
          <p>
            Separately from the chat, platform staff may ask the model to draft a workflow plan for one of this firm's closes, sending their instruction with the close's
            client, month, sign-off state and counts of its preparation work; nothing runs before staff approve the plan.
          </p>
        }
        open={open}
        onClose={() => setOpen(false)}
        readOnly={!policy.can_manage_agent_chat}
        ready
        saveLabel={policy.staff_planning ? "Stop staff plan drafts" : "Allow staff plan drafts"}
        onSave={async (reason) => {
          await change({ staff_planning: !policy.staff_planning }, reason);
          setSaved("Saved.");
          refresh();
        }}
      />
    </SettingCard>
  );
}

// The firm's rules, each a card: where it stands, one line on what that means, and for an administrator an Edit.
export function Policies({ policy, refresh }: { policy: Policy; refresh: () => void }) {
  const manages = policy.can_manage || policy.can_manage_links || policy.can_manage_agent_chat || policy.can_manage_ledger_writes;
  return (
    <>
      {!manages && <p className="note">A firm administrator changes these rules; every change is kept with its reason. Details opens what each rule means.</p>}
      <SectionHeader title="Review and posting" />
      <SettingGrid>
        <IndependentReview policy={policy} refresh={refresh} />
        <LedgerWrites policy={policy} refresh={refresh} />
        <Materiality policy={policy} refresh={refresh} />
      </SettingGrid>
      <SectionHeader title="Clients and requests" />
      <SettingGrid>
        <LinkPasscodes policy={policy} refresh={refresh} />
        <ReminderRule policy={policy} refresh={refresh} />
      </SettingGrid>
      <SectionHeader title="The agent and the model" />
      <SettingGrid>
        <AgentChatSetting policy={policy} refresh={refresh} />
        <StaffPlanning policy={policy} refresh={refresh} />
        {policy.ocr_provider && (
          <SettingCard icon="file" tone="blue" title="Reading PDFs" status={policy.ocr_provider.split(/ reads | \(|;|:/)[0]} statusTone="info" summary={policy.ocr_provider}>
            <p>Chosen by the server operator.</p>
          </SettingCard>
        )}
      </SettingGrid>
    </>
  );
}
