import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Card } from "@close/ui/components/Card";
import { Dialog, DialogActions } from "@close/ui/components/Dialog";
import { useBrowserHash } from "../../data/browser";
import { useDates } from "../../data/time";
import { money, workspaceRequest } from "../closes/workspace";

export type XeroConnection = { id: string; state: string; tenant_name: string; tenants: { tenantId: string; tenantName: string }[]; error: string };
export type XeroGrant = { id: string; connection_id: string; tenant_id: string; account_ids: string[]; kinds: string[]; max_amount: string; expires_at: string; reason: string; granted_by: string };
type Client = {
  entity: string;
  can_connect: boolean;
  can_grant: boolean;
  xero: XeroConnection[];
  xero_configured?: boolean;
  xero_grants?: XeroGrant[];
  profile: { currency: string } | null;
  // Which connectors may be connected for the firm: Xero switched off offers no new connection.
  connectable?: { quickbooks: boolean; xero: boolean };
};

// The connection a step acts on: the one Xero just returned for, else the one waiting for its organisation, else the
// first (under the client's own name first, as the read lists them).
const current = (client: Client, returning?: string | null) =>
  (returning ? client.xero.find((x) => x.state === "authorizing" && x.id === returning) : undefined) ?? client.xero.find((x) => x.state === "choose_tenant") ?? client.xero[0];

const path = (name: string, what = "xero") => `/client/${what}?name=${encodeURIComponent(name)}`;

// What a person does next with the client's Xero connection, from the Books card (docs/books-sources.md, Client page):
// connect an organisation, finish the sign-in Xero returned with, choose which authorised organisation is the client's,
// or disconnect it. Each step is a dialog with the person's confirmation that they act for this client, as the
// connection routes require. Xero returns to this page with its answer in the address's fragment.
export function XeroAction({ client, onChanged }: { client: Client; onChanged: () => void }) {
  const name = client.entity;
  const hash = useBrowserHash();
  const answer = useMemo(() => new URLSearchParams(hash.slice(1)), [hash]);
  const c = current(client, answer.has("xero_code") ? answer.get("xero_connection") : undefined);
  const returned = answer.has("xero_code") && c?.state === "authorizing";
  const [open, setOpen] = useState<"" | "connect" | "finish" | "choose" | "manage">("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [agreed, setAgreed] = useState(false);
  const [tenant, setTenant] = useState("");
  // The connection whose organisation is being chosen: the one waiting for it, or the one picked in Manage.
  const [choosing, setChoosing] = useState<XeroConnection | undefined>(undefined);
  const chosen = choosing ?? (c?.state === "choose_tenant" ? c : undefined);
  // Xero's answer opens the step that finishes it.
  // Switched off for the firm, Xero makes no new connection; those already made are still managed.
  const off = client.connectable?.xero === false;
  useEffect(() => {
    if (returned && !off) setOpen("finish");
  }, [returned, off]);
  if (!client.can_connect) return null;
  const close = () => {
    setOpen("");
    setError("");
    setAgreed(false);
    setChoosing(undefined);
  };
  async function act(action: string, connection: XeroConnection | undefined, extra: Record<string, unknown> = {}) {
    setBusy(true);
    setError("");
    try {
      const result = await workspaceRequest(path(name), { action, confirmed: agreed, connection_id: connection?.id ?? "", ...extra });
      if (action === "connect") {
        window.location.assign(result.url);
        return;
      }
      if (action === "callback") window.history.replaceState(null, "", window.location.pathname + window.location.search);
      close();
      onChanged();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Xero did not take the change. Refresh before trying again.");
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
  const button =
    c?.state === "choose_tenant" && !off ? (
      <button type="button" onClick={() => setOpen("choose")}>
        Choose the organisation
      </button>
    ) : c ? (
      <button type="button" onClick={() => setOpen("manage")}>
        Manage Xero
      </button>
    ) : off ? null : (
      <button type="button" onClick={() => setOpen("connect")}>
        Connect Xero
      </button>
    );
  return (
    <>
      {button}
      <Dialog
        open={open === "connect"}
        onClose={close}
        title="Connect Xero"
        description={`Sign in to Xero and authorise ${name}’s organisation. Its books are then read for each month; nothing posts without a separate grant.`}
      >
        {error && <p role="alert">{error}</p>}
        {!client.xero_configured && <p className="note">Xero is not set up on this server yet.</p>}
        {agree(<>I may connect {name}’s books for the firm.</>)}
        {actions(
          <button type="button" className="primary" disabled={busy || !agreed || !client.xero_configured} onClick={() => void act("connect", undefined)}>
            {busy ? "Opening Xero…" : "Continue to Xero"}
          </button>,
        )}
      </Dialog>
      <Dialog
        open={open === "finish" && returned}
        onClose={close}
        title="Finish connecting Xero"
        description={`Xero returned from the sign-in. Finish to see which organisations it authorised for ${name}.`}
      >
        {error && <p role="alert">{error}</p>}
        {agree(<>I started this sign-in for {name}.</>)}
        {actions(
          <button type="button" className="primary" disabled={busy || !agreed} onClick={() => void act("callback", c, { code: answer.get("xero_code"), state: answer.get("xero_state") })}>
            {busy ? "Finishing…" : "Finish connecting"}
          </button>,
        )}
      </Dialog>
      {chosen && (
        <Dialog open={open === "choose"} onClose={close} title="Choose the organisation" description={`Which organisation authorised in this sign-in is ${name}’s? Only it is read.`}>
          {error && <p role="alert">{error}</p>}
          <fieldset>
            <legend>Organisation</legend>
            {chosen.tenants.map((t) => (
              <label key={t.tenantId}>
                <input type="radio" name="tenant" value={t.tenantId} checked={tenant === t.tenantId} onChange={() => setTenant(t.tenantId)} /> {t.tenantName}
              </label>
            ))}
          </fieldset>
          {agree(<>The organisation I chose is {name}’s.</>)}
          {actions(
            <>
              <button type="button" className="danger" disabled={busy || !agreed} onClick={() => void act("disconnect", chosen)}>
                Disconnect
              </button>
              <button type="button" className="primary" disabled={busy || !agreed || !tenant} onClick={() => void act("confirm", chosen, { tenant_id: tenant })}>
                {busy ? "Saving…" : "Use this organisation"}
              </button>
            </>,
          )}
        </Dialog>
      )}
      {c && (
        <Dialog open={open === "manage"} onClose={close} title={`Xero for ${name}`} description="Each connected organisation, where it stands, and what you can do with it.">
          {error && <p role="alert">{error}</p>}
          {client.xero.map((x) => (
            <div className="ks-connection" key={x.id}>
              <p>
                <b>{x.tenant_name || "Organisation not chosen yet"}</b> · {stateWords[x.state] ?? x.state.replaceAll("_", " ")}
              </p>
              {x.error && <p role="alert">{x.error}</p>}
              {x.state === "authorizing" && <p className="note">The sign-in was not finished. Disconnect it to start again.</p>}
              {x.state === "reconnect_required" && <p className="note">Xero no longer accepts this sign-in. Disconnect it, then connect again.</p>}
              {off && x.state === "choose_tenant" && <p className="note">Xero is switched off for your firm, so this sign-in cannot be finished. Disconnect it.</p>}
              <div className="setting-actions">
                {x.state === "choose_tenant" && !off && (
                  <button
                    type="button"
                    onClick={() => {
                      setChoosing(x);
                      setAgreed(false);
                      setOpen("choose");
                    }}
                  >
                    Choose the organisation
                  </button>
                )}
                <button type="button" className="danger" disabled={busy || !agreed} onClick={() => void act("disconnect", x)}>
                  Disconnect
                </button>
              </div>
            </div>
          ))}
          <p className="note">Disconnecting stops reading and removes the stored sign-in. Remove the app under Connected Apps in Xero as well to withdraw its access there.</p>
          {agree(<>I act for {name} on its Xero connection.</>)}
          <DialogActions>
            <button type="button" onClick={close}>
              Close
            </button>
          </DialogActions>
        </Dialog>
      )}
    </>
  );
}

const stateWords: Record<string, string> = {
  authorizing: "sign-in not finished",
  exchanging: "finishing the sign-in",
  choose_tenant: "waiting for you to choose the organisation",
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
  ["bill_correction", "Bill line recodes"],
];

// What may post to the client's Xero organisation: the bounded grant a member with posting approval gives (the Xero
// accounts, the kinds of draft, a limit and an expiry), or none. Posting itself stays a separate decision on each draft.
export function XeroPostingCard({ client, connection, onChanged }: { client: Client; connection: string; onChanged: () => void }) {
  const dates = useDates();
  const name = client.entity;
  // One Xero connection and its grant: a month posts only through the grant on the organisation it reads its books
  // from, so a client with two organisations connected has a card for each.
  const c = client.xero.find((x) => x.id === connection);
  const grant = client.xero_grants?.find((g) => g.connection_id === connection);
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
  if (!c && !grant) return null;
  const ready = c?.state === "active";
  // The organisation the grant is for, named on the card and in its dialogs, so a client with two connected tells them
  // apart.
  const organisation = c?.tenant_name || (grant?.tenant_id ? `organisation ${grant.tenant_id}` : "");
  const target = organisation ? `the Xero organisation ${organisation}` : `${name}’s Xero organisation`;
  const close = () => {
    setOpen("");
    setError("");
    setPhrase("");
  };
  async function change(action: "grant" | "revoke") {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(path(name, "xero-grant"), {
        action,
        connection_id: c?.id ?? "",
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
        title={organisation ? `Xero posting · ${organisation}` : "Xero posting"}
        chip={grant ? <span className="pill">Granted</span> : <span className="pill neutral">Not granted</span>}
        action={
          client.can_grant ? (
            grant ? (
              <button type="button" onClick={() => setOpen("revoke")}>
                Revoke
              </button>
            ) : (
              <button type="button" disabled={!ready} title={ready ? undefined : "Connect and choose the client's Xero organisation first"} onClick={() => setOpen("grant")}>
                Grant
              </button>
            )
          ) : undefined
        }
      >
        {grant
          ? `Approved ${grant.kinds.map((k) => (labels[k] ?? k).toLowerCase()).join(", ")} may post to ${grant.account_ids.length} Xero accounts, up to ${money(grant.max_amount, currency)} each, until ${dates.date(grant.expires_at)}.`
          : `Nothing posts to ${target}: there is no grant. Each posting is still approved on its own.`}
      </Card>
      <Dialog
        open={open === "grant"}
        onClose={close}
        title="Grant Xero posting"
        description={`Which approved drafts may post to ${target}, to which accounts, up to what, and until when.`}
      >
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void change("grant");
          }}
        >
          {error && <p role="alert">{error}</p>}
          <label>
            Xero account IDs, from a month’s Trial Balance (2 to 20)
            <textarea value={accounts} onChange={(e) => setAccounts(e.target.value)} />
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
            Most per journal ({currency})
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
            Type GRANT XERO POSTING
            <input value={phrase} onChange={(e) => setPhrase(e.target.value)} autoComplete="off" />
          </label>
          <DialogActions>
            <button type="button" onClick={close}>
              Cancel
            </button>
            <button className="primary" disabled={busy || !ready || phrase !== "GRANT XERO POSTING"}>
              {busy ? "Granting…" : "Grant posting"}
            </button>
          </DialogActions>
        </form>
      </Dialog>
      {grant && (
        <Dialog open={open === "revoke"} onClose={close} title="Revoke the grant" description={`Approved drafts stop posting to ${target} at once. Postings already made stay.`}>
          {error && <p role="alert">{error}</p>}
          <p className="note">
            Granted by {grant.granted_by}: {grant.reason}
          </p>
          <label>
            Type REVOKE XERO POSTING
            <input value={phrase} onChange={(e) => setPhrase(e.target.value)} autoComplete="off" />
          </label>
          <DialogActions>
            <button type="button" onClick={close}>
              Cancel
            </button>
            <button type="button" className="danger" disabled={busy || phrase !== "REVOKE XERO POSTING"} onClick={() => void change("revoke")}>
              {busy ? "Revoking…" : "Revoke the grant"}
            </button>
          </DialogActions>
        </Dialog>
      )}
    </>
  );
}
