import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Card } from "@close/ui/components/Card";
import { Dialog, DialogActions } from "@close/ui/components/Dialog";
import { useBrowserHash } from "../../data/browser";
import { useDates } from "../../data/time";
import { money, workspaceRequest } from "../closes/workspace";

export type XeroConnection = { id: string; state: string; tenant_name: string; tenants: { tenantId: string; tenantName: string }[]; error: string };
export type XeroGrant = { id: string; tenant_id: string; account_ids: string[]; kinds: string[]; max_amount: string; expires_at: string; reason: string; granted_by: string };
type Client = { entity: string; can_connect: boolean; can_grant: boolean; xero: XeroConnection | null; xero_configured?: boolean; xero_grant?: XeroGrant | null; profile: { currency: string } | null };

const path = (name: string, what = "xero") => `/client/${what}?name=${encodeURIComponent(name)}`;

// What a person does next with the client's Xero connection, from the Books card (docs/books-sources.md, Client page):
// connect an organisation, finish the sign-in Xero returned with, choose which authorised organisation is the client's,
// or disconnect it. Each step is a dialog with the person's confirmation that they act for this client, as the
// connection routes require. Xero returns to this page with its answer in the address's fragment.
export function XeroAction({ client, onChanged }: { client: Client; onChanged: () => void }) {
  const name = client.entity;
  const hash = useBrowserHash();
  const answer = useMemo(() => new URLSearchParams(hash.slice(1)), [hash]);
  const c = client.xero;
  const returned = answer.has("xero_code") && c?.state === "authorizing";
  const [open, setOpen] = useState<"" | "connect" | "finish" | "choose" | "manage">("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [agreed, setAgreed] = useState(false);
  const [tenant, setTenant] = useState("");
  // Xero's answer opens the step that finishes it.
  useEffect(() => {
    if (returned) setOpen("finish");
  }, [returned]);
  if (!client.can_connect) return null;
  const close = () => {
    setOpen("");
    setError("");
    setAgreed(false);
  };
  async function act(action: string, extra: Record<string, unknown> = {}) {
    setBusy(true);
    setError("");
    try {
      const result = await workspaceRequest(path(name), { action, confirmed: agreed, connection_id: c?.id ?? "", ...extra });
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
    c?.state === "choose_tenant" ? (
      <button type="button" onClick={() => setOpen("choose")}>
        Choose the organisation
      </button>
    ) : c ? (
      <button type="button" onClick={() => setOpen("manage")}>
        Manage Xero
      </button>
    ) : (
      <button type="button" onClick={() => setOpen("connect")}>
        Connect Xero
      </button>
    );
  return (
    <>
      {button}
      <Dialog open={open === "connect"} onClose={close} title="Connect Xero" description={`Sign in to Xero and authorise ${name}’s organisation. Its books are then read for each month; nothing posts without a separate grant.`}>
        {error && <p role="alert">{error}</p>}
        {!client.xero_configured && <p className="note">Xero is not set up on this server yet.</p>}
        {agree(<>I may connect {name}’s books for the firm.</>)}
        {actions(
          <button type="button" className="primary" disabled={busy || !agreed || !client.xero_configured} onClick={() => void act("connect")}>
            {busy ? "Opening Xero…" : "Continue to Xero"}
          </button>,
        )}
      </Dialog>
      <Dialog open={open === "finish" && returned} onClose={close} title="Finish connecting Xero" description={`Xero returned from the sign-in. Finish to see which organisations it authorised for ${name}.`}>
        {error && <p role="alert">{error}</p>}
        {agree(<>I started this sign-in for {name}.</>)}
        {actions(
          <button type="button" className="primary" disabled={busy || !agreed} onClick={() => void act("callback", { code: answer.get("xero_code"), state: answer.get("xero_state") })}>
            {busy ? "Finishing…" : "Finish connecting"}
          </button>,
        )}
      </Dialog>
      {c?.state === "choose_tenant" && (
        <Dialog open={open === "choose"} onClose={close} title="Choose the organisation" description={`Which organisation authorised in this sign-in is ${name}’s? Only it is read.`}>
          {error && <p role="alert">{error}</p>}
          <fieldset>
            <legend>Organisation</legend>
            {c.tenants.map((t) => (
              <label key={t.tenantId}>
                <input type="radio" name="tenant" value={t.tenantId} checked={tenant === t.tenantId} onChange={() => setTenant(t.tenantId)} /> {t.tenantName}
              </label>
            ))}
          </fieldset>
          {agree(<>The organisation I chose is {name}’s.</>)}
          {actions(
            <>
              <button type="button" className="danger" disabled={busy || !agreed} onClick={() => void act("disconnect")}>
                Disconnect
              </button>
              <button type="button" className="primary" disabled={busy || !agreed || !tenant} onClick={() => void act("confirm", { tenant_id: tenant })}>
                {busy ? "Saving…" : "Use this organisation"}
              </button>
            </>,
          )}
        </Dialog>
      )}
      {c && (
        <Dialog open={open === "manage"} onClose={close} title={`Xero for ${name}`} description="The connected organisation, where it stands, and how to disconnect it.">
          {error && <p role="alert">{error}</p>}
          <p>
            <b>{c.tenant_name || "Organisation not chosen yet"}</b> · {stateWords[c.state] ?? c.state.replaceAll("_", " ")}
          </p>
          {c.error && <p role="alert">{c.error}</p>}
          {c.state === "authorizing" && <p className="note">The sign-in was not finished. Disconnect it to start again.</p>}
          {c.state === "reconnect_required" && <p className="note">Xero no longer accepts this sign-in. Disconnect it, then connect again.</p>}
          <p className="note">Disconnecting stops reading and removes the stored sign-in. Remove the app under Connected Apps in Xero as well to withdraw its access there.</p>
          {agree(<>I act for {name} on its Xero connection.</>)}
          {actions(
            <button type="button" className="danger" disabled={busy || !agreed} onClick={() => void act("disconnect")}>
              {busy ? "Disconnecting…" : "Disconnect"}
            </button>,
          )}
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
export function XeroPostingCard({ client, onChanged }: { client: Client; onChanged: () => void }) {
  const dates = useDates();
  const name = client.entity;
  const c = client.xero;
  const grant = client.xero_grant;
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
        title="Xero posting"
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
          : `Nothing posts to ${name}’s Xero organisation: there is no grant. Each posting is still approved on its own.`}
      </Card>
      <Dialog open={open === "grant"} onClose={close} title="Grant Xero posting" description={`Which approved drafts may post to ${name}’s Xero organisation, to which accounts, up to what, and until when.`}>
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
        <Dialog open={open === "revoke"} onClose={close} title="Revoke the grant" description={`Approved drafts stop posting to ${name}’s Xero organisation at once. Postings already made stay.`}>
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
