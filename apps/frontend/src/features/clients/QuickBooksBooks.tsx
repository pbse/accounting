import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Card } from "@close/ui/components/Card";
import { Dialog, DialogActions } from "@close/ui/components/Dialog";
import { useBrowserHash } from "../../data/browser";
import { useDates } from "../../data/time";
import { money, workspaceRequest } from "../closes/workspace";

// A connection is held under the name of the month it was made from (client_entity), which may be the client's alias; a
// production company's name must match that name.
export type QuickBooksConnection = { id: string; client_entity: string; environment: "sandbox" | "production"; state: string; realm: string; company_name: string; error: string };
export type ProductionGrant = { id: string; realm: string; company: string; account_ids: string[]; kinds: string[]; max_amount: string; expires_at: string; reason: string; granted_by: string; connection_id: string };
type Client = {
  entity: string;
  can_connect: boolean;
  can_grant: boolean;
  quickbooks: QuickBooksConnection[];
  quickbooks_setup?: { sandbox: boolean; production: boolean };
  production_grants?: ProductionGrant[];
  profile: { currency: string } | null;
  // Which connectors may be connected for the firm: QuickBooks switched off offers no new connection.
  connectable?: { quickbooks: boolean; xero: boolean };
};

const path = (name: string, what = "quickbooks") => `/client/${what}?name=${encodeURIComponent(name)}`;
const environments = { production: "QuickBooks Online", sandbox: "QuickBooks sandbox" };

// What a person does next with the client's QuickBooks connection, from the Books card (docs/books-sources.md, Client
// page): connect a company, finish the sign-in Intuit returned with, confirm the company, or manage it (refresh its
// sign-in, disconnect it). Each step is a dialog with the person's confirmation that they act for this client, as the
// connection routes require. Intuit returns to this page with its answer in the address's fragment.
export function QuickBooksAction({ client, onChanged }: { client: Client; onChanged: () => void }) {
  const name = client.entity;
  const hash = useBrowserHash();
  const answer = useMemo(() => new URLSearchParams(hash.slice(1)), [hash]);
  // The attempt Intuit returned for, named in the answer.
  const returned = answer.has("qbo_code") ? client.quickbooks.find((c) => c.state === "authorizing" && c.id === answer.get("qbo_connection")) : undefined;
  const pending = client.quickbooks.find((c) => c.state === "pending_confirmation");
  const [open, setOpen] = useState<"" | "connect" | "finish" | "confirm" | "manage">("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [agreed, setAgreed] = useState(false);
  // Switched off for the firm, QuickBooks makes no new connection; those already made are still managed.
  const off = client.connectable?.quickbooks === false;
  const setup = off ? { sandbox: false, production: false } : (client.quickbooks_setup ?? { sandbox: false, production: false });
  // The environments this server can connect that the client has no connection in yet: each can be added beside the other.
  const missing = (["production", "sandbox"] as const).filter((env) => setup[env] && !client.quickbooks.some((c) => c.environment === env));
  const [environment, setEnvironment] = useState<"production" | "sandbox">(setup.production || !setup.sandbox ? "production" : "sandbox");
  // Intuit's answer opens the step that finishes it.
  useEffect(() => {
    if (returned && !off) setOpen("finish");
  }, [returned?.id, off]);
  if (!client.can_connect) return null;
  const close = () => {
    setOpen("");
    setError("");
    setAgreed(false);
  };
  async function act(action: string, connection?: QuickBooksConnection, extra: Record<string, unknown> = {}) {
    setBusy(true);
    setError("");
    try {
      const result = await workspaceRequest(path(name), { action, confirmed: agreed, connection_id: connection?.id ?? "", realm: connection?.realm ?? "", ...extra });
      if (action === "connect") {
        window.location.assign(result.url);
        return;
      }
      if (action === "callback") window.history.replaceState(null, "", window.location.pathname + window.location.search);
      close();
      onChanged();
    } catch (e) {
      setError(e instanceof Error ? e.message : "QuickBooks did not take the change. Refresh before trying again.");
      onChanged();
    } finally {
      setBusy(false);
    }
  }
  const agree = (text: ReactNode) => (
    <label>
      <input type="checkbox" checked={agreed} onChange={(e) => setAgreed(e.target.checked)} /> {text}
    </label>
  );
  const actions = (primary: ReactNode) => (
    <DialogActions>
      <button type="button" onClick={close}>
        Cancel
      </button>
      {primary}
    </DialogActions>
  );
  const button = pending && !off ? (
    <button type="button" className="button" onClick={() => setOpen("confirm")}>
      Confirm the company
    </button>
  ) : client.quickbooks.length ? (
    <button type="button" className="button" onClick={() => setOpen("manage")}>
      Manage QuickBooks
    </button>
  ) : off ? null : (
    <button type="button" className="button" onClick={() => setOpen("connect")}>
      Connect QuickBooks
    </button>
  );
  return (
    <>
      {button}
      <Dialog
        open={open === "connect"}
        onClose={close}
        title="Connect QuickBooks"
        description={`Sign in to Intuit and choose ${name}’s company. Its books are then read for each month; nothing posts without a separate grant.`}
      >
        {error && <p role="alert">{error}</p>}
        {!setup.production && !setup.sandbox ? (
          <p className="note">QuickBooks is not set up on this server yet.</p>
        ) : (
          <fieldset>
            <legend>Company</legend>
            {(["production", "sandbox"] as const).map((env) => (
              <label key={env}>
                <input type="radio" name="environment" checked={environment === env} disabled={!setup[env]} onChange={() => setEnvironment(env)} /> {environments[env]}
                {!setup[env] && " (not set up on this server)"}
              </label>
            ))}
          </fieldset>
        )}
        {agree(<>I may connect {name}’s books for the firm.</>)}
        {actions(
          <button type="button" className="primary" disabled={busy || !agreed || !setup[environment]} onClick={() => void act("connect", undefined, { environment })}>
            {busy ? "Opening Intuit…" : "Continue to Intuit"}
          </button>,
        )}
      </Dialog>
      <Dialog
        open={open === "finish" && !!returned}
        onClose={close}
        title="Finish connecting QuickBooks"
        description={`Intuit returned with company ${answer.get("qbo_realm") ?? ""}. Finish to read its name and settings for ${name}.`}
      >
        {error && <p role="alert">{error}</p>}
        {agree(<>I started this sign-in for {name}.</>)}
        {actions(
          <button
            type="button"
            className="primary"
            disabled={busy || !agreed}
            onClick={() => void act("callback", returned, { code: answer.get("qbo_code"), state: answer.get("qbo_state"), realm: answer.get("qbo_realm") })}
          >
            {busy ? "Finishing…" : "Finish connecting"}
          </button>,
        )}
      </Dialog>
      {pending && (
        <Dialog
          open={open === "confirm"}
          onClose={close}
          title="Confirm the company"
          description={`Intuit connected ${pending.company_name || "a company"} (company ${pending.realm}). Its books are read for ${name} only once you confirm it is theirs.`}
        >
          {error && <p role="alert">{error}</p>}
          {pending.environment === "production" && pending.company_name !== pending.client_entity && (
            <p className="note">
              The company is named “{pending.company_name}”, not “{pending.client_entity}”. A production company’s name must match the client’s exactly before its books are read; rename one of them,
              or disconnect and choose another company.
            </p>
          )}
          {agree(
            <>
              {pending.company_name || "This company"} is {name}’s.
            </>,
          )}
          {actions(
            <>
              <button type="button" className="danger" disabled={busy || !agreed} onClick={() => void act("disconnect", pending)}>
                Disconnect
              </button>
              <button
                type="button"
                className="primary"
                disabled={busy || !agreed || (pending.environment === "production" && pending.company_name !== pending.client_entity)}
                onClick={() => void act("confirm", pending)}
              >
                {busy ? "Confirming…" : "Confirm"}
              </button>
            </>,
          )}
        </Dialog>
      )}
      <Dialog open={open === "manage"} onClose={close} title={`QuickBooks for ${name}`} description="Each connected company, where it stands, and what you can do with it.">
        {error && <p role="alert">{error}</p>}
        {client.quickbooks.map((c) => (
          <div className="ks-connection" key={c.id}>
            <p>
              <b>{environments[c.environment]}</b> · {c.company_name || "company not read yet"}
              {c.realm && ` · company ${c.realm}`} · {stateWords[c.state] ?? c.state.replaceAll("_", " ")}
            </p>
            {c.error && <p role="alert">{c.error}</p>}
            {c.state === "authorizing" && <p className="note">The sign-in was not finished. Disconnect it to start again.</p>}
            {c.state === "reconnect_required" && <p className="note">Intuit no longer accepts this sign-in. Disconnect it, then connect again.</p>}
            <div className="setting-actions">
              {c.state === "active" && (
                <button type="button" disabled={busy || !agreed} onClick={() => void act("refresh", c)}>
                  Refresh the sign-in
                </button>
              )}
              <button type="button" className="danger" disabled={busy || !agreed} onClick={() => void act("disconnect", c)}>
                Disconnect
              </button>
            </div>
          </div>
        ))}
        {missing.length > 0 && (
          <div className="setting-actions">
            {missing.map((env) => (
              <button
                key={env}
                type="button"
                onClick={() => {
                  setEnvironment(env);
                  setAgreed(false);
                  setOpen("connect");
                }}
              >
                Connect {environments[env]}
              </button>
            ))}
          </div>
        )}
        <p className="note">Disconnecting stops reading and removes the stored sign-in. Remove the app in Intuit as well to withdraw its access there.</p>
        {agree(<>I act for {name} on its QuickBooks connection.</>)}
        <DialogActions>
          <button type="button" onClick={close}>
            Close
          </button>
        </DialogActions>
      </Dialog>
    </>
  );
}

const stateWords: Record<string, string> = {
  authorizing: "sign-in not finished",
  exchanging: "finishing the sign-in",
  pending_confirmation: "waiting for you to confirm the company",
  active: "connected",
  refreshing: "connected, refreshing",
  reconnect_required: "needs connecting again",
};

const kinds: [string, string][] = [
  ["prepaid", "Prepaid schedules"],
  ["accrual", "Accrued expenses"],
  ["loan", "Loan schedules"],
  ["owner", "Owner items"],
  ["payroll", "Payroll entries"],
  ["settlement", "Stripe fee entries"],
  ["bill_correction", "Bill expense corrections"],
  ["purchase_correction", "Expense line recodes"],
  ["deposit_correction", "Income deposit line recodes"],
  ["payment_application", "Customer payment applications"],
  ["credit_application", "Vendor credit applications"],
  ["vendor_credit", "Supplier credits"],
];

// What may post to the client's production QuickBooks company (docs/design-system.md, Card): the bounded grant a
// member with posting approval gives (the accounts, the kinds of draft, a limit and an expiry), or none. Posting itself
// stays a separate decision on each draft.
export function PostingCard({ client, connection, onChanged }: { client: Client; connection: string; onChanged: () => void }) {
  const dates = useDates();
  const name = client.entity;
  // One production connection and its grant: a month posts only through the grant on the connection it reads its
  // books from, so a client with two companies connected has a card for each.
  const production = client.quickbooks.find((c) => c.id === connection && c.environment === "production");
  const grant = client.production_grants?.find((g) => g.connection_id === connection);
  const currency = client.profile?.currency ?? "USD";
  const [open, setOpen] = useState<"" | "grant" | "revoke">("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [accounts, setAccounts] = useState("");
  const [chosen, setChosen] = useState<string[]>(["prepaid"]);
  const [limit, setLimit] = useState("");
  const [expiry, setExpiry] = useState("");
  const [reason, setReason] = useState("");
  const [phrase, setPhrase] = useState("");
  if (!production && !grant) return null;
  const ready = production?.state === "active" && production.company_name === production.client_entity;
  // The company the grant is for, named on the card and in its dialogs, so a client with two connected tells them apart.
  const realm = production?.realm || grant?.realm || "";
  const company = `${production?.company_name || grant?.company || name}’s QuickBooks company${realm ? ` (realm ${realm})` : ""}`;
  const close = () => {
    setOpen("");
    setError("");
    setPhrase("");
  };
  async function change(action: "grant" | "revoke") {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(path(name, "production-grant"), {
        action,
        connection_id: production?.id ?? "",
        grant_id: grant?.id ?? "",
        account_ids: accounts.split(/[\s,]+/).filter(Boolean),
        kinds: chosen,
        max_amount: limit,
        expires_at: expiry ? new Date(expiry).toISOString() : undefined,
        reason,
        confirmation: phrase,
      });
      close();
      onChanged();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The grant's outcome is unclear. Refresh before deciding again.");
      onChanged();
    } finally {
      setBusy(false);
    }
  }
  const labels = Object.fromEntries(kinds);
  return (
    <>
      <Card
        icon="shield"
        tone={grant ? "reply" : "done"}
        title={`Posting · ${production?.company_name || grant?.company || "QuickBooks"}${realm ? ` (${realm})` : ""}`}
        chip={grant ? <span className="pill">Granted</span> : <span className="pill neutral">Not granted</span>}
        action={
          client.can_grant ? (
            grant ? (
              <button type="button" onClick={() => setOpen("revoke")}>
                Revoke
              </button>
            ) : (
              <button type="button" disabled={!ready} title={ready ? undefined : "Connect and confirm the production company, named as the client is, first"} onClick={() => setOpen("grant")}>
                Grant
              </button>
            )
          ) : undefined
        }
      >
        {grant
          ? `Approved ${grant.kinds.map((k) => (labels[k] ?? k).toLowerCase()).join(", ")} may post to QuickBooks accounts ${grant.account_ids.join(", ")}, up to ${money(grant.max_amount, currency)} each, until ${dates.date(grant.expires_at)}.`
          : `Nothing posts to ${company}: there is no grant. Each posting is still approved on its own.`}
      </Card>
      <Dialog
        open={open === "grant"}
        onClose={close}
        title="Grant production posting"
        description={`Which approved drafts may post to ${company}, to which accounts, up to what, and until when.`}
      >
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void change("grant");
          }}
        >
          {error && <p role="alert">{error}</p>}
          <label>
            Account IDs from the Chart of Accounts (2 to 20)
            <input value={accounts} onChange={(e) => setAccounts(e.target.value)} placeholder="30, 31, 32" />
          </label>
          <fieldset>
            <legend>Drafts it covers</legend>
            {kinds.map(([kind, label]) => (
              <label key={kind}>
                <input type="checkbox" checked={chosen.includes(kind)} onChange={(e) => setChosen(e.target.checked ? [...chosen, kind] : chosen.filter((k) => k !== kind))} /> {label}
              </label>
            ))}
          </fieldset>
          <label>
            Most per journal, bill line or supplier credit ({currency})
            <input value={limit} onChange={(e) => setLimit(e.target.value)} inputMode="decimal" />
          </label>
          <label>
            Until (within 30 days)
            <input type="datetime-local" value={expiry} onChange={(e) => setExpiry(e.target.value)} />
          </label>
          <label>
            Reason
            <input value={reason} maxLength={2000} onChange={(e) => setReason(e.target.value)} />
          </label>
          <label>
            Type GRANT PRODUCTION POSTING
            <input value={phrase} onChange={(e) => setPhrase(e.target.value)} autoComplete="off" />
          </label>
          <DialogActions>
            <button type="button" onClick={close}>
              Cancel
            </button>
            <button className="primary" disabled={busy || !ready || phrase !== "GRANT PRODUCTION POSTING"}>
              {busy ? "Granting…" : "Grant posting"}
            </button>
          </DialogActions>
        </form>
      </Dialog>
      {grant && (
        <Dialog open={open === "revoke"} onClose={close} title="Revoke the grant" description={`Approved drafts stop posting to ${company} at once. Postings already made stay.`}>
          {error && <p role="alert">{error}</p>}
          <p className="note">
            Granted by {grant.granted_by}: {grant.reason}
          </p>
          <label>
            Type REVOKE PRODUCTION GRANT
            <input value={phrase} onChange={(e) => setPhrase(e.target.value)} autoComplete="off" />
          </label>
          <DialogActions>
            <button type="button" onClick={close}>
              Cancel
            </button>
            <button type="button" className="danger" disabled={busy || phrase !== "REVOKE PRODUCTION GRANT"} onClick={() => void change("revoke")}>
              {busy ? "Revoking…" : "Revoke the grant"}
            </button>
          </DialogActions>
        </Dialog>
      )}
    </>
  );
}
