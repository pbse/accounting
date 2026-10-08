import { useEffect, useState } from "react";
import { Link } from "react-router";
import { PageHeader } from "@close/ui/components/PageHeader";
import { AgentAccess } from "./AgentAccess";
import { isEnrollmentError } from "./ClosesList";
import { EmailSettings } from "./Email";
import { FirmEnrollment } from "./Enterprise";
import { ClientRoster } from "./Roster";
import { workspaceRequest } from "./workspace";

type AgentChat = "off" | "closes" | "firm";
type Policy = { model_provider: string; independent_review: boolean; independent_review_min_amount: string; materiality_amount: string; link_passcode: boolean; reminder_after_days: number; reminder_max: number; agent_chat: AgentChat; hosted: boolean; can_manage: boolean; can_manage_links: boolean; can_manage_agent_chat: boolean; ledger_writes: boolean; can_manage_ledger_writes: boolean };

// Who on our team supports the firm, and the firm's own records to take away at exit.
function SupportAndExit() {
  const [owner, setOwner] = useState<string>();
  const [unread, setUnread] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let live = true;
    workspaceRequest("/organization/policy")
      .then((data: { support_owner: string }) => live && setOwner(data.support_owner))
      .catch((e) => live && setUnread(e instanceof Error ? e.message : "The support owner could not be read."));
    return () => {
      live = false;
    };
  }, []);
  async function download() {
    setBusy(true);
    setMessage("");
    try {
      const link = document.createElement("a");
      link.href = URL.createObjectURL(await workspaceRequest("/firm-export", undefined, true));
      link.download = `firm-export-${new Date().toISOString().slice(0, 10)}.json`;
      link.click();
      setTimeout(() => URL.revokeObjectURL(link.href), 60_000);
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "The firm export is unavailable.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="Support and exit">
      <h2>Support and your records</h2>
      {unread ? <p role="alert">{unread}</p> : owner === undefined ? null : <p>{owner ? `Your support owner is ${owner}.` : "No support owner is named yet."}</p>}
      <p>
        At any time, and when you leave, a data administrator can download the firm's own records: settings, people and access history, procedures, client
        records, and every close with where its review package downloads.
      </p>
      {message && <p role="alert">{message}</p>}
      <button type="button" disabled={busy} onClick={() => void download()}>Download the firm's records</button>
    </section>
  );
}

function IndependentReview() {
  const [policy, setPolicy] = useState<Policy>();
  const [reason, setReason] = useState("");
  const [minimum, setMinimum] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest("/organization/policy")
      .then((data) => live && setPolicy(data))
      .catch((e) => live && setMessage(e instanceof Error ? e.message : "Firm policy is unavailable."));
    return () => {
      live = false;
    };
  }, [refresh]);
  if (!policy) return message ? <p role="alert">{message}</p> : null;
  async function change(input: object = { independent_review: !policy!.independent_review }) {
    setBusy(true);
    setMessage("");
    try {
      await workspaceRequest("/organization/policy", { ...input, reason });
      setReason("");
      setMinimum("");
      setRefresh((n) => n + 1);
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Policy change failed.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="Independent review policy">
      <h2>Independent review</h2>
      <p>
        {policy.independent_review
          ? "On: nobody may approve a draft they prepared or confirmed, drafts without a recorded preparer cannot be approved, and production posting needs an approval that meets this rule."
          : policy.independent_review_min_amount
            ? `Only for large drafts: a draft whose proposed entries total at least ${policy.independent_review_min_amount} (in the draft's own currency) needs a reviewer who did not prepare or confirm it; smaller drafts may be approved by their preparer.`
            : "Off: any member with the reviewer role may approve a draft, including one they prepared."}
      </p>
      {!policy.hosted ? (
        <p className="note">Local development shares one identity, so this policy can only be enforced for hosted firms.</p>
      ) : policy.can_manage ? (
        <>
          <label>
            Reason for the change
            <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
          </label>
          <button type="button" disabled={busy || reason.trim().length < 5} onClick={() => void change()}>
            {policy.independent_review ? "Turn off independent review" : "Require independent review"}
          </button>
          <label>
            Or require it only for drafts whose proposed entries total at least
            <input value={minimum} inputMode="decimal" placeholder={policy.independent_review_min_amount || "1000.00"} onChange={(e) => setMinimum(e.target.value)} />
          </label>
          <button type="button" disabled={busy || reason.trim().length < 5 || !minimum.trim()} onClick={() => void change({ independent_review: false, independent_review_min_amount: minimum })}>
            {policy.independent_review ? "Require it only for large drafts" : "Set minimum"}
          </button>
          {policy.independent_review_min_amount && (
            <button type="button" disabled={busy || reason.trim().length < 5} onClick={() => void change({ independent_review_min_amount: "" })}>
              Remove minimum
            </button>
          )}
        </>
      ) : (
        <p className="note">A firm administrator with org:manage can change this policy.</p>
      )}
      {message && <p role="alert">{message}</p>}
    </section>
  );
}

// Writing approved entries to clients' live books is the firm's own decision: off until a firm administrator turns it
// on, with a reason. QuickBooks sandbox companies are outside it.
function LedgerWrites() {
  const [policy, setPolicy] = useState<Policy>();
  const [reason, setReason] = useState("");
  const [message, setMessage] = useState("");
  const [done, setDone] = useState("");
  const [busy, setBusy] = useState(false);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest("/organization/policy")
      .then((data) => live && setPolicy(data))
      .catch((e) => live && setMessage(e instanceof Error ? e.message : "Firm policy is unavailable."));
    return () => {
      live = false;
    };
  }, [refresh]);
  if (!policy) return message ? <p role="alert">{message}</p> : null;
  async function change() {
    setBusy(true);
    setMessage("");
    setDone("");
    try {
      const result: { ledger_writes: boolean; writes_in_flight: number } = await workspaceRequest("/organization/policy", { ledger_writes: !policy!.ledger_writes, reason });
      // Turning writing off withdraws approvals not yet acted on; writes already under way finish and are read back.
      if (!result.ledger_writes)
        setDone(
          `Writing is off. Live writes approved and not started need a fresh approval once it is back on.${
            result.writes_in_flight ? ` ${result.writes_in_flight} ${result.writes_in_flight === 1 ? "write was" : "writes were"} already under way; ${result.writes_in_flight === 1 ? "it finishes" : "they finish"} and ${result.writes_in_flight === 1 ? "is" : "are"} read back.` : ""
          }`,
        );
      setReason("");
      setRefresh((n) => n + 1);
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Policy change failed.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="Writing to clients' books">
      <h2>Writing to clients' books</h2>
      <p>
        {policy.ledger_writes
          ? "On: an approved entry can be written to a client's live QuickBooks or Xero company by a member allowed to post, under that client's posting grant and only for the kinds this server allows."
          : "Off: nothing is written to any client's live QuickBooks or Xero company. Approved entries are prepared for a person to book in the ledger."}{" "}
        QuickBooks sandbox companies are not affected, and a write already made is still read back after writing is turned off.
      </p>
      {policy.can_manage_ledger_writes ? (
        <>
          <label>
            Reason for the change
            <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
          </label>
          <button type="button" disabled={busy || reason.trim().length < 5} onClick={() => void change()}>
            {policy.ledger_writes ? "Turn writing off" : "Turn writing on"}
          </button>
        </>
      ) : (
        <p className="note">A firm administrator with org:manage can change this setting.</p>
      )}
      {done && <p role="status">{done}</p>}
      {message && <p role="alert">{message}</p>}
    </section>
  );
}

function LinkPasscodes() {
  const [policy, setPolicy] = useState<Policy>();
  const [reason, setReason] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest("/organization/policy")
      .then((data) => live && setPolicy(data))
      .catch((e) => live && setMessage(e instanceof Error ? e.message : "Firm policy is unavailable."));
    return () => {
      live = false;
    };
  }, [refresh]);
  if (!policy) return message ? <p role="alert">{message}</p> : null;
  async function change() {
    setBusy(true);
    setMessage("");
    try {
      await workspaceRequest("/organization/policy", { link_passcode: !policy!.link_passcode, reason });
      setReason("");
      setRefresh((n) => n + 1);
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Policy change failed.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="Client link passcodes">
      <h2>Client link passcodes</h2>
      <p>
        {policy.link_passcode
          ? "On: each new client request link also needs an 8-digit passcode. The person who issues the link sees it once and tells the client by phone or in person; five wrong tries close the link."
          : "Off: anyone holding a client request link can read the request and reply."}{" "}
        Links issued earlier keep working as they were issued.
      </p>
      {policy.can_manage_links ? (
        <>
          <label>
            Reason for the change
            <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
          </label>
          <button type="button" disabled={busy || reason.trim().length < 5} onClick={() => void change()}>
            {policy.link_passcode ? "Stop requiring passcodes" : "Require passcodes on new links"}
          </button>
        </>
      ) : (
        <p className="note">A firm administrator with org:manage can change this policy.</p>
      )}
      {message && <p role="alert">{message}</p>}
    </section>
  );
}

// Each reminder still needs a person's approval; the rule says when one is due and how many a request can have.
// The agent chat sends a member's question and the reads the model chooses to the configured provider, so it stays
// off until an administrator turns it on, for client closes or firm-wide, with a reason kept in the policy history.
const agentLevels: { value: AgentChat; label: string; detail: string }[] = [
  { value: "off", label: "Off", detail: "Questions are answered only from the assistant's fixed reads and commands; nothing is sent to a model." },
  { value: "closes", label: "On for client closes", detail: "A question inside a client close may be investigated and answered by the model, from reads of that close made with the member's own permissions. Questions asked outside a close are never sent." },
  { value: "firm", label: "On firm-wide", detail: "As for client closes, and a question asked outside a close may be answered from the member's own lists across the clients they can open, with client names." },
];

function AgentChatSetting() {
  const [policy, setPolicy] = useState<Policy>();
  const [reason, setReason] = useState("");
  const [choice, setChoice] = useState<AgentChat>();
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest("/organization/policy")
      .then((data) => live && setPolicy(data))
      .catch((e) => live && setMessage(e instanceof Error ? e.message : "Firm policy is unavailable."));
    return () => {
      live = false;
    };
  }, [refresh]);
  if (!policy) return message ? <p role="alert">{message}</p> : null;
  const chosen = choice ?? policy.agent_chat;
  async function change() {
    setBusy(true);
    setMessage("");
    try {
      await workspaceRequest("/organization/policy", { agent_chat: chosen, reason });
      setReason("");
      setChoice(undefined);
      setRefresh((n) => n + 1);
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Policy change failed.");
    } finally {
      setBusy(false);
    }
  }
  const current = agentLevels.find((l) => l.value === policy.agent_chat) ?? agentLevels[0];
  return (
    <section aria-label="Agent chat">
      <h2>Agent chat</h2>
      <p>
        <strong>{current.label}.</strong> {current.detail}{" "}
        When on, the question, the model's reads and the conversation's earlier turns go to {policy.model_provider}, with card, bank and sign-in details removed; the reads are made with the member's own permissions, listed under the answer and kept with the attempt's record without their content. An answer is kept only if every figure and claim in it is bound to a read that was sent; it links those reads and says it was written by the model. The model reads and proposes only: an action it proposes runs after the person confirms its card. Each read and each answer counts against the firm's daily model limit.
      </p>
      {policy.can_manage_agent_chat ? (
        <>
          <fieldset>
            <legend>Level</legend>
            {agentLevels.map((level) => (
              <label key={level.value} className="checkbox-label">
                <input type="radio" name="agent-chat" value={level.value} checked={chosen === level.value} onChange={() => setChoice(level.value)} /> {level.label}
              </label>
            ))}
          </fieldset>
          <label>
            Reason for the change
            <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
          </label>
          <button type="button" disabled={busy || reason.trim().length < 5 || chosen === policy.agent_chat} onClick={() => void change()}>
            Apply
          </button>
        </>
      ) : (
        <p className="note">A firm administrator with org:manage can change this policy.</p>
      )}
      {message && <p role="alert">{message}</p>}
    </section>
  );
}

// How the firm's PDFs are read, as the document worker reports it; the server operator sets it, so it is shown only.
function DocumentReading() {
  const [provider, setProvider] = useState("");
  useEffect(() => {
    let live = true;
    workspaceRequest("/organization/policy")
      .then((data: { ocr_provider?: string }) => live && setProvider(data.ocr_provider ?? ""))
      .catch(() => live && setProvider(""));
    return () => {
      live = false;
    };
  }, []);
  if (!provider) return null;
  return (
    <section aria-label="Reading PDFs">
      <h2>Reading PDFs</h2>
      <p>{provider}.</p>
      <p className="note">
        A page's own PDF text is exact and always kept; a machine reading of the page only checks it, and the page is flagged when the two differ. Scanned pages read by a machine are labelled to check
        against the original. The server operator chooses the reader.
      </p>
    </section>
  );
}

function ReminderRule() {
  const [policy, setPolicy] = useState<Policy>();
  const [after, setAfter] = useState(0);
  const [most, setMost] = useState(3);
  const [reason, setReason] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest("/organization/policy")
      .then((data: Policy) => {
        if (!live) return;
        setPolicy(data);
        setAfter(data.reminder_after_days);
        setMost(data.reminder_max);
      })
      .catch((e) => live && setMessage(e instanceof Error ? e.message : "Firm policy is unavailable."));
    return () => {
      live = false;
    };
  }, [refresh]);
  if (!policy) return message ? <p role="alert">{message}</p> : null;
  async function change() {
    setBusy(true);
    setMessage("");
    try {
      await workspaceRequest("/organization/policy", { reminder_after_days: after, reminder_max: most, reason });
      setReason("");
      setRefresh((n) => n + 1);
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Policy change failed.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="Reminder rule">
      <h2>Reminder rule</h2>
      <p>
        A reminder becomes due {policy.reminder_after_days === 0 ? "as soon as" : `${policy.reminder_after_days} ${policy.reminder_after_days === 1 ? "day" : "days"} after`} the previous email is delivered, and a request can have {policy.reminder_max} {policy.reminder_max === 1 ? "reminder" : "reminders"}. Each reminder still needs a person's approval. After the last one, or once the private link expires, contact the client another way.
      </p>
      {policy.can_manage_links ? (
        <>
          <label>
            Days after delivery
            <select value={after} onChange={(e) => setAfter(Number(e.target.value))}>
              {[0, 1, 2, 3, 4, 5, 6].map((n) => <option key={n} value={n}>{n}</option>)}
            </select>
          </label>
          <label>
            Reminders per request
            <select value={most} onChange={(e) => setMost(Number(e.target.value))}>
              {[1, 2, 3, 4, 5].map((n) => <option key={n} value={n}>{n}</option>)}
            </select>
          </label>
          <label>
            Reason for the change
            <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
          </label>
          <button type="button" disabled={busy || reason.trim().length < 5} onClick={() => void change()}>Save reminder rule</button>
        </>
      ) : (
        <p className="note">A firm administrator with org:manage can change this policy.</p>
      )}
      {message && <p role="alert">{message}</p>}
    </section>
  );
}

function Materiality() {
  const [policy, setPolicy] = useState<Policy>();
  const [amount, setAmount] = useState("");
  const [reason, setReason] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest("/organization/policy")
      .then((data) => live && setPolicy(data))
      .catch((e) => live && setMessage(e instanceof Error ? e.message : "Firm policy is unavailable."));
    return () => {
      live = false;
    };
  }, [refresh]);
  if (!policy) return message ? <p role="alert">{message}</p> : null;
  async function change(materiality_amount: string) {
    setBusy(true);
    setMessage("");
    try {
      await workspaceRequest("/organization/policy", { materiality_amount, reason });
      setReason("");
      setAmount("");
      setRefresh((n) => n + 1);
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Policy change failed.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="Materiality">
      <h2>Materiality</h2>
      <p>
        {policy.materiality_amount
          ? `${policy.materiality_amount}: a balance-sheet account whose Trial Balance differs from its support by less than this, with no proposed entry pending, is shown as within materiality instead of a difference to explain. The comparison is per account, in the client's currency. It does not approve a draft or waive an entry.`
          : "Not set: every Trial Balance difference is shown as a difference to explain."}
      </p>
      {policy.can_manage_links ? (
        <>
          <label>
            Materiality amount
            <input value={amount} inputMode="decimal" placeholder={policy.materiality_amount || "500.00"} onChange={(e) => setAmount(e.target.value)} />
          </label>
          <label>
            Reason for the change
            <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
          </label>
          <button type="button" disabled={busy || reason.trim().length < 5 || !amount.trim()} onClick={() => void change(amount)}>Set materiality</button>
          {policy.materiality_amount && <button type="button" disabled={busy || reason.trim().length < 5} onClick={() => void change("")}>Clear materiality</button>}
        </>
      ) : (
        <p className="note">A firm administrator with org:manage can change this policy.</p>
      )}
      {message && <p role="alert">{message}</p>}
    </section>
  );
}

export default function Firm() {
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let live = true;
    setError("");
    workspaceRequest("")
      .catch((e) => live && setError(e instanceof Error ? e.message : "Firm settings are unavailable."))
      .finally(() => live && setLoaded(true));
    return () => {
      live = false;
    };
  }, [refresh]);
  return (
    <>
      <PageHeader
        icon="firm"
        title="Firm settings"
        actions={
          <div className="title-actions">
            <Link className="button" to="/dashboard/security">
              Security
            </Link>
            <Link className="button" to="/dashboard/measure">
              Human effort
            </Link>
          </div>
        }
      >
        Review policies, client link and reminder rules, your agent access and approved email delivery for the whole firm.
      </PageHeader>
      {error && <p role="alert">{error}</p>}
      {isEnrollmentError(error) && <FirmEnrollment onEnrolled={() => setRefresh((n) => n + 1)} />}
      {loaded && !error && (
        <div className="stack">
          <IndependentReview />
          <LedgerWrites />
      <LinkPasscodes />
      <AgentChatSetting />
      <DocumentReading />
      <Materiality />
      <ReminderRule />
      <ClientRoster />
      <SupportAndExit />
          <p>
            People, client assignments, agent grants, connected services and recent changes are in the <Link to="/dashboard/security">Security center</Link>. Request or give up your own agent access here.
          </p>
          <AgentAccess />
          <EmailSettings />
        </div>
      )}
    </>
  );
}
