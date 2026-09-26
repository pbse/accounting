import { useEffect, useRef, useState } from "react";
import { useLocation } from "react-router";
import { workspaceRequest } from "./workspace";

type Tenant = { tenantId: string; tenantName: string };
type Balance = { account_id: string; code: string; name: string; class: string; debit: string; credit: string; ytd_debit: string; ytd_credit: string };
type Status = {
  configured: boolean;
  hosted: boolean;
  connection: { id: string; state: string; tenant_name: string; tenants: Tenant[]; error: string } | null;
  snapshots: { id: string; period: string; captured_at: string; digest: string }[];
  latest: { currency: string; date: string; rows: Balance[]; transactions?: { unread: string; counts?: Record<string, number> } } | { error: string } | null;
};

export function Xero({ closeId, entity }: { closeId: string; entity: string }) {
  const [status, setStatus] = useState<Status>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [tenant, setTenant] = useState("");
  const [callback] = useState(() => new URLSearchParams(window.location.hash.slice(1)));
  async function load() {
    try {
      setStatus(await workspaceRequest(`/${closeId}/xero`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Xero status unavailable.");
    }
  }
  useEffect(() => {
    void load();
  }, [closeId]);
  async function action(name: string) {
    setBusy(true);
    setError("");
    try {
      const result = await workspaceRequest(`/${closeId}/xero`, {
        action: name,
        connection_id: status?.connection?.id ?? "",
        confirmed,
        tenant_id: name === "confirm" ? tenant : undefined,
        code: name === "callback" ? callback.get("xero_code") : undefined,
        state: name === "callback" ? callback.get("xero_state") : undefined,
      });
      if (name === "connect") {
        window.location.assign(result.url);
        return;
      }
      if (name === "callback") {
        // Keep the code in the address until the exchange succeeds, so a reload can retry it.
        window.history.replaceState(null, "", window.location.pathname + window.location.search);
        callback.delete("xero_code");
        callback.delete("xero_state");
      }
      setConfirmed(false);
      setStatus(result);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The Xero action did not complete.");
      await load();
    } finally {
      setBusy(false);
    }
  }
  async function download(id: string) {
    try {
      const url = URL.createObjectURL(await workspaceRequest(`/${closeId}/xero/snapshots/${id}`, undefined, true));
      const link = document.createElement("a");
      link.href = url;
      link.download = `xero-trial-balance-${id}.json`;
      link.click();
      window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Download failed.");
    }
  }
  const c = status?.connection;
  const latest = status?.latest;
  const confirm = (
    <label>
      <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /> I am authorised to connect {entity}'s Xero organisation for month-end evidence and, only with a separate grant and approval, reviewed journals.
    </label>
  );
  return (
    <details id="xero" open={callback.has("xero_code") || window.location.hash === "#xero"}>
      <summary>Xero month-end Trial Balance</summary>
      <p>This reads the organisation, its chart of accounts and the Trial Balance at month-end. It writes to Xero only an approved draft's manual journal, under a client grant, after a person approves and a person posts it. Xero data is not used to train models.</p>
      {error && <p role="alert">{error}</p>}
      {!status ? null : !status.configured ? (
        <p className="empty">Xero is not configured on this server. Set the Xero app's client ID, secret and callback URL first.</p>
      ) : !status.hosted ? (
        <p className="empty">Connecting Xero needs a signed-in firm session with integrations access.</p>
      ) : !c ? (
        <>
          {confirm}
          <button disabled={busy || !confirmed} onClick={() => void action("connect")}>
            Connect Xero
          </button>
        </>
      ) : c.state === "authorizing" && callback.has("xero_code") ? (
        <>
          {confirm}
          <button disabled={busy || !confirmed} onClick={() => void action("callback")}>
            Finish connecting Xero
          </button>
        </>
      ) : c.state === "choose_tenant" ? (
        <fieldset>
          <legend>Which Xero organisation belongs to {entity}?</legend>
          {c.tenants.map((t) => (
            <label key={t.tenantId}>
              <input type="radio" name="xero-tenant" value={t.tenantId} checked={tenant === t.tenantId} onChange={() => setTenant(t.tenantId)} /> {t.tenantName}
            </label>
          ))}
          {confirm}
          <button disabled={busy || !confirmed || !tenant} onClick={() => void action("confirm")}>
            Use this organisation
          </button>
        </fieldset>
      ) : c.state === "active" ? (
        <>
          <p>
            Connected to <strong>{c.tenant_name}</strong>.
          </p>
          {confirm}
          <button disabled={busy || !confirmed} onClick={() => void action("snapshot")}>
            {busy ? "Reading Xero…" : "Capture month-end Trial Balance"}
          </button>
          <XeroGrant closeId={closeId} connectionId={c.id} />
          <XeroBillCorrection closeId={closeId} />
        </>
      ) : (
        <>
          <p role="status">
            Connection {c.state.replaceAll("_", " ")}. {c.error} Disconnect, then connect again.
          </p>
          {confirm}
        </>
      )}
      {c && c.error && c.state === "active" && <p role="status">Last attempt: {c.error}</p>}
      {latest && "error" in latest && <p role="alert">The latest snapshot could not be read: {latest.error}</p>}
      {latest && "rows" in latest && (
        <>
          <p>
            Trial Balance as at {latest.date}, in {latest.currency || "the organisation's base currency"}. YTD columns are Xero's: the balance at that date for asset, liability and equity accounts, and the movement since the start of the financial year for revenue and expense accounts.
          </p>
          {latest.transactions?.counts ? (
            <p className="note">
              Transactions read with it:{" "}
              {Object.entries(latest.transactions.counts)
                .map(([kind, n]) => `${n} ${kind.replace(/([a-z])([A-Z])/g, "$1 $2").toLowerCase()}`)
                .join(", ")}
              .
            </p>
          ) : (
            latest.transactions?.unread && <p role="status">Transactions were not read: {latest.transactions.unread}</p>
          )}
          <table>
            <thead>
              <tr>
                <th>Code</th>
                <th>Account</th>
                <th>Class</th>
                <th>Debit</th>
                <th>Credit</th>
                <th>YTD Debit</th>
                <th>YTD Credit</th>
              </tr>
            </thead>
            <tbody>
              {latest.rows.map((row) => (
                <tr key={row.account_id}>
                  <td>{row.code}</td>
                  <td>{row.name}</td>
                  <td>{row.class || "Not in chart"}</td>
                  <td>{row.debit}</td>
                  <td>{row.credit}</td>
                  <td>{row.ytd_debit}</td>
                  <td>{row.ytd_credit}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
      {status?.snapshots.map((s) => (
        <p key={s.id}>
          Snapshot #{s.id} for {s.period}, captured {new Date(s.captured_at).toLocaleString()}.{" "}
          <button onClick={() => void download(s.id)}>Download exact JSON</button>
        </p>
      ))}
      {c && (
        <button disabled={busy || !confirmed} onClick={() => void action("disconnect")}>
          Disconnect Xero
        </button>
      )}
    </details>
  );
}

type Grant = { id?: string; state?: string; account_ids?: string[]; kinds?: string[]; max_amount?: string; expires_at?: string; reason?: string };

// A person's grant lets approved drafts of the listed kinds post to the listed Xero accounts, up to a limit, until expiry.
function XeroGrant({ closeId, connectionId }: { closeId: string; connectionId: string }) {
  const [grant, setGrant] = useState<Grant>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [accounts, setAccounts] = useState("");
  const [kinds, setKinds] = useState<string[]>(["prepaid"]);
  const [limit, setLimit] = useState("");
  const [expiry, setExpiry] = useState("");
  const [reason, setReason] = useState("");
  const [phrase, setPhrase] = useState("");
  async function load() {
    try {
      setGrant(await workspaceRequest(`/${closeId}/xero-grant`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Xero grant unavailable.");
    }
  }
  useEffect(() => {
    void load();
  }, [closeId]);
  async function act(action: "grant" | "revoke") {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/xero-grant`, {
        action, connection_id: connectionId, grant_id: grant?.id, account_ids: accounts.split(/[\s,]+/).filter(Boolean), kinds, max_amount: limit,
        expires_at: expiry ? new Date(expiry).toISOString() : undefined, reason, confirmation: phrase,
      });
      setPhrase("");
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Grant outcome unclear. Refresh before trying again.");
      await load();
    } finally {
      setBusy(false);
    }
  }
  return (
    <details>
      <summary>Xero posting grant</summary>
      {error && <p role="alert">{error}</p>}
      {grant?.id ? (
        <>
          <p>
            Grant #{grant.id}: {grant.kinds?.join(", ")} drafts to {grant.account_ids?.length} accounts, up to {grant.max_amount} per journal, until{" "}
            {grant.expires_at ? new Date(grant.expires_at).toLocaleString() : "—"}. {grant.reason}
          </p>
          <label>
            Type REVOKE XERO POSTING
            <input value={phrase} onChange={(e) => setPhrase(e.target.value)} autoComplete="off" />
          </label>
          <button disabled={busy || phrase !== "REVOKE XERO POSTING"} onClick={() => void act("revoke")}>
            Revoke grant
          </button>
        </>
      ) : (
        <>
          <p>No grant: nothing can be posted to this Xero organisation. A grant lists the Xero account IDs (from the Trial Balance above), the draft kinds, a limit per journal and an expiry within 30 days.</p>
          <label>
            Xero account IDs (2–20)
            <textarea value={accounts} onChange={(e) => setAccounts(e.target.value)} />
          </label>
          <fieldset>
            <legend>Draft kinds</legend>
            {["prepaid", "accrual", "loan", "owner", "settlement", "bill_correction"].map((kind) => (
              <label key={kind}>
                <input type="checkbox" checked={kinds.includes(kind)} onChange={(e) => setKinds(e.target.checked ? [...kinds, kind] : kinds.filter((k) => k !== kind))} /> {kind}
              </label>
            ))}
          </fieldset>
          <label>
            Limit per journal
            <input value={limit} inputMode="decimal" onChange={(e) => setLimit(e.target.value)} />
          </label>
          <label>
            Expires
            <input type="datetime-local" value={expiry} onChange={(e) => setExpiry(e.target.value)} />
          </label>
          <label>
            Reason
            <input value={reason} maxLength={2000} onChange={(e) => setReason(e.target.value)} />
          </label>
          <label>
            Type GRANT XERO POSTING
            <input value={phrase} onChange={(e) => setPhrase(e.target.value)} autoComplete="off" />
          </label>
          <button disabled={busy || phrase !== "GRANT XERO POSTING"} onClick={() => void act("grant")}>
            Grant Xero posting
          </button>
        </>
      )}
    </details>
  );
}

type XeroLine = { LineItemID: string; Description?: string; AccountCode?: string; LineAmount: number | string };
type XeroFix = { state: string; digest?: string; target_line_id?: string; target_account_id?: string; target_account_code?: string; reason?: string; target_account_name?: string; rule_reference?: string; approved_by?: string; error?: string; before_bill?: { LineItems: XeroLine[] } };

// One reviewed recode of one Xero bill line, under the client's Xero grant.
function XeroBillCorrection({ closeId }: { closeId: string }) {
  const [bills, setBills] = useState<{ id: string; number: string; date: string; total: string; currency: string }[]>([]);
  const [selected, setSelected] = useState("");
  const [item, setItem] = useState<XeroFix>();
  const [lineId, setLineId] = useState("");
  const [account, setAccount] = useState("");
  const [reason, setReason] = useState("");
  const [rule, setRule] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const base = `/${closeId}/xero/bills`;
  const panel = useRef<HTMLDetailsElement>(null);
  const { hash } = useLocation();
  useEffect(() => {
    const asked = /^#correct-xero-([\w-]+)$/.exec(hash)?.[1];
    if (!asked || !panel.current) return;
    panel.current.open = true;
    panel.current.scrollIntoView({ block: "start" });
    void select(asked);
  }, [hash]);
  async function load() {
    try {
      const result = await workspaceRequest(`${base}/candidates`);
      setBills(result.items);
      setMessage(result.message ?? "");
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Candidates unavailable.");
    }
  }
  async function select(id: string) {
    setSelected(id); setItem(undefined); setLineId(""); setConfirmed(false); setMessage("");
    if (!id) return;
    try {
      const current = await workspaceRequest(`${base}/${encodeURIComponent(id)}/correction`);
      setItem(current);
      setLineId(current.target_line_id ?? "");
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Correction status unavailable.");
    }
  }
  async function run(action: string) {
    setBusy(true);
    setMessage("");
    try {
      setItem(await workspaceRequest(`${base}/${encodeURIComponent(selected)}/correction`, { action, digest: item?.digest, reason, rule_reference: rule, target_line_id: lineId, target_account_id: account, confirmed }));
      setConfirmed(false);
      setMessage(action === "execute" || action === "verify" ? "Xero readback verified. Capture Xero again before relying on its figures." : "Saved exact correction state.");
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Correction outcome unclear. Refresh before any retry.");
    } finally {
      setBusy(false);
    }
  }
  const editable = !item || item.state === "not_prepared" || item.state === "preview" || item.state === "approved";
  return (
    <details ref={panel} onToggle={(e) => { if (e.currentTarget.open && bills.length === 0) void load(); }}>
      <summary>Recode a Xero bill line</summary>
      <p>For an approved bill in the close month with nothing paid or credited, no tax and up to ten lines. Choose one line and a new expense account under the accountant's cited rule; both accounts must be in a Xero grant that covers bill corrections. Everything else must read back unchanged, and an uncertain update is never retried.</p>
      <label>
        Bill from the latest Xero capture
        <select value={selected} onChange={(e) => void select(e.target.value)}>
          <option value="">Choose one</option>
          {selected && !bills.some((b) => b.id === selected) && <option value={selected}>Bill {selected}</option>}
          {bills.map((b) => <option key={b.id} value={b.id}>{b.number || b.id} · {b.date} · {b.currency} {b.total}</option>)}
        </select>
      </label>
      {item && item.state !== "not_prepared" && <p>State: {item.state}. Line {item.target_line_id} → {item.target_account_name} ({item.target_account_code}, account ID {item.target_account_id}). Reason: {item.reason} Rule: {item.rule_reference}. {item.approved_by && `Approved by ${item.approved_by}.`}</p>}
      {item?.error && <p role="alert">{item.error}</p>}
      {selected && editable && (
        <>
          <label>
            Line
            <select value={lineId} onChange={(e) => setLineId(e.target.value)}>
              <option value="">Choose the exact line</option>
              {item?.before_bill?.LineItems.map((l) => <option key={l.LineItemID} value={l.LineItemID}>{l.LineAmount} · account {l.AccountCode}{l.Description ? ` · ${l.Description}` : ""}</option>)}
            </select>
          </label>
          <label>
            New Xero expense account ID
            <input value={account} onChange={(e) => setAccount(e.target.value)} />
          </label>
          <label>
            Accountant reason and source reference
            <textarea value={reason} maxLength={2000} onChange={(e) => setReason(e.target.value)} />
          </label>
          <label>
            Accountant's coding rule it follows
            <input value={rule} maxLength={200} onChange={(e) => setRule(e.target.value)} />
          </label>
          <label>
            <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /> I reviewed the bill, the capture, the rule and the exact new account.
          </label>
          <button disabled={busy || !confirmed || !lineId || !account || reason.trim().length < 5 || rule.trim().length < 5} onClick={() => void run("preview")}>Preview exact correction</button>
        </>
      )}
      {item?.state === "preview" && <button disabled={busy || !confirmed} onClick={() => void run("approve")}>Approve this correction for 10 minutes</button>}
      {item?.state === "approved" && <button disabled={busy || !confirmed} onClick={() => void run("execute")}>Update the bill once</button>}
      {(item?.state === "executing" || item?.state === "unknown" || item?.state === "verified") && <button disabled={busy} onClick={() => void run("verify")}>Read back from Xero</button>}
      {message && <p role="status">{message}</p>}
    </details>
  );
}
