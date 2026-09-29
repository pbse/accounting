import { useEffect, useState } from "react";
import { Link } from "react-router";
import { Icon } from "../dashboard/Icon";
import { AgentAccess } from "./AgentAccess";
import { isEnrollmentError } from "./ClosesList";
import { EmailSettings } from "./Email";
import { FirmEnrollment } from "./Enterprise";
import { workspaceRequest } from "./workspace";

type Policy = { independent_review: boolean; independent_review_min_amount: string; materiality_amount: string; link_passcode: boolean; reminder_after_days: number; reminder_max: number; model_answers: boolean; firm_answers: boolean; investigations: boolean; hosted: boolean; can_manage: boolean; can_manage_links: boolean; can_manage_model_answers: boolean };

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
// Written answers send a close's saved reads to OpenAI, so they stay off until an administrator turns them on.
function WrittenAnswers() {
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
  async function change(setting: "model_answers" | "firm_answers" | "investigations") {
    setBusy(true);
    setMessage("");
    try {
      await workspaceRequest("/organization/policy", { [setting]: !policy![setting], reason });
      setReason("");
      setRefresh((n) => n + 1);
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Policy change failed.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="Written assistant answers">
      <h2>Written assistant answers</h2>
      <p>
        {policy.model_answers
          ? "On: when a question in a client close matches none of the assistant's fixed reads, the model writes an answer."
          : "Off: questions in a client close are answered only from the assistant's fixed reads and are never sent."}{" "}
        When on, the question and that close's saved status, sign-off blockers, tie-out, changes and exceptions (and the open draft) go to OpenAI, with card, bank and sign-in details removed and a request not to store them. An answer is kept only if each read it cites was sent whole and holds the words it quotes; it links those reads and says it was written by the model. Each attempt counts against the firm's daily model limit.
      </p>
      <p>
        {policy.firm_answers
          ? "Firm-wide answers are on: a question asked outside a client close that matches none of the fixed reads is also answered by the model."
          : "Firm-wide answers are off: questions asked outside a client close are never sent."}{" "}
        When on, the question and the member's own lists of what needs a person today, the review queue and blocked work, across the clients they can open, go to OpenAI under the same removals and checks. The answer is shown once and never kept in the conversation.
      </p>
      <p>
        {policy.investigations
          ? "Investigations are on: before writing a close answer, the model may first read that close's own records through the assistant's read-only tools."
          : "Investigations are off: a written answer rests only on the close's saved status, blockers, tie-out, changes and exceptions."}{" "}
        When on, the model may make up to six reads of this close (a document's text, a draft, the tie-out, changes, exceptions, a search), each made with your own permissions, redacted the same way and sent to OpenAI unstored; each read counts against the firm's daily model limit, the answer lists what was read, and the reads are kept with the attempt's record. It reads only; it never changes, approves, sends or posts anything. Written answers must be on.
      </p>
      {policy.can_manage_model_answers ? (
        <>
          <label>
            Reason for the change
            <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
          </label>
          <button type="button" disabled={busy || reason.trim().length < 5} onClick={() => void change("model_answers")}>
            {policy.model_answers ? "Stop written answers" : "Turn on written answers"}
          </button>
          <button type="button" disabled={busy || reason.trim().length < 5} onClick={() => void change("firm_answers")}>
            {policy.firm_answers ? "Stop firm-wide answers" : "Turn on firm-wide answers"}
          </button>
          <button type="button" disabled={busy || reason.trim().length < 5 || !policy.model_answers} onClick={() => void change("investigations")}>
            {policy.investigations ? "Stop investigations" : "Turn on investigations"}
          </button>
        </>
      ) : (
        <p className="note">A firm administrator with org:manage can change this policy.</p>
      )}
      {message && <p role="alert">{message}</p>}
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
      <div className="page-title">
        <div>
          <span className="kpi-icon" aria-hidden="true">
            <Icon name="firm" />
          </span>
          <span>
            <h1>Firm settings</h1>
            <p>Review policies, client link and reminder rules, your agent access and approved email delivery for the whole firm.</p>
          </span>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      {isEnrollmentError(error) && <FirmEnrollment onEnrolled={() => setRefresh((n) => n + 1)} />}
      {loaded && !error && (
        <div className="stack">
          <IndependentReview />
      <LinkPasscodes />
      <WrittenAnswers />
      <Materiality />
      <ReminderRule />
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
