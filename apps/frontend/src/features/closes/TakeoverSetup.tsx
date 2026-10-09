import { useDates } from "../../data/time";
import { type FormEvent, useEffect, useState } from "react";
import { Link } from "react-router";
import { workspaceRequest } from "./workspace";
import { SectionHeader } from "@close/ui/components/SectionHeader";

type Content = {
  close: { entity: string; period: string };
  companies: { provider: string; name: string; company: string; environment: string; state: string }[];
  procedures: { id: string; kind: string; method: string; enabled: boolean; scope: string; expense_account: string; prepaid_account: string }[];
  assigned: { subject: string; member: boolean }[];
  routine: { finish: string; due_days: number | null } | null;
  scope: { unavailable?: string; items?: { name: string; state: string }[] };
};
type Setup = { id: string; digest: string; timezone: string; confirmed_by: string; confirmed_at: string };
type View = { takeover: boolean; content?: Content; digest?: string; problems?: string[]; setups?: Setup[]; you?: string[] };

const plain = (s: string) => s.replaceAll("_", " ");

// The opening month's takeover setup: what the client is taken over with, read from the firm's records and confirmed once.
export function TakeoverSetup({ closeId }: { closeId: string }) {
  // The device's zone is the browser's own, read once the page has hydrated: a server render would name its own.
  const [device, setDevice] = useState("");
  const [zones, setZones] = useState<string[]>([]);
  useEffect(() => {
    try {
      setDevice(Intl.DateTimeFormat().resolvedOptions().timeZone ?? "");
      setZones(Intl.supportedValuesOf("timeZone"));
    } catch {
      // No zone to suggest; the person types it.
    }
  }, []);
  const dates = useDates();
  const [view, setView] = useState<View>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [version, setVersion] = useState(0);
  const [editing, setEditing] = useState(false);
  useEffect(() => {
    let live = true;
    workspaceRequest(`/${closeId}/takeover-setup`)
      .then((data) => live && (setView(data), setError("")))
      .catch((e) => live && (setView(undefined), setError(e instanceof Error ? e.message : "The takeover setup is unavailable.")));
    return () => {
      live = false;
    };
  }, [closeId, version]);
  if (!view) return error ? <p role="alert">{error}</p> : null;
  if (!view.takeover || !view.content) return null;
  const { content } = view;
  const latest = view.setups?.[0];
  const confirm = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/takeover-setup`, { digest: view.digest, timezone: String(form.get("timezone") ?? "").trim(), confirmed: form.has("confirmed") });
      setVersion((n) => n + 1);
      setEditing(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "That could not be saved.");
    } finally {
      setBusy(false);
    }
  };
  const settings = `/dashboard/closes/${closeId}?tab=settings`;
  const overview = `/dashboard/closes/${closeId}?tab=overview`;
  return (
    <section aria-label="Takeover setup">
      <SectionHeader title="Takeover setup" />
      <p className="note">
        This is {content.close.entity}'s first close in the firm ({content.close.period}). Confirm once what it is taken over with; each item is changed where it is kept.
      </p>
      {error && <p role="alert">{error}</p>}
      <dl>
        <dt>Company the books are read from</dt>
        <dd>
          {content.companies.length === 0 ? "None connected." : content.companies.map((c) => `${c.provider} · ${c.name || c.company}${c.environment ? ` (${c.environment})` : ""} · ${plain(c.state)}`).join("; ")} <Link to={settings}>Connections</Link>
        </dd>
        <dt>Firm procedures in force</dt>
        <dd>
          {content.procedures.length === 0 ? "None approved." : (
            <ul>
              {content.procedures.map((p) => (
                <li key={p.id}>
                  {plain(p.kind)}{p.method && `: ${plain(p.method)}`} · {p.scope === "client" ? "client override (an exception to the firm default)" : "firm default"}{p.expense_account && ` · expense account ${p.expense_account}`}{p.prepaid_account && ` · prepaid account ${p.prepaid_account}`}{!p.enabled && " · off"}
                </li>
              ))}
            </ul>
          )}{" "}
          <Link to={settings}>Approved procedures and client overrides</Link>
        </dd>
        <dt>Assigned to this client</dt>
        <dd>
          {content.assigned.length === 0 ? "No one is assigned." : content.assigned.map((a) => (a.member ? a.subject : `${a.subject} (membership not granted, so no access)`)).join(", ")}. Members whose role gives them every client also see it; what each
          may do comes from their role.
        </dd>
        <dt>What you may do</dt>
        <dd>{(view.you ?? []).join("; ")}.</dd>
        <dt>Account scope proposed from this month's capture</dt>
        <dd>
          {content.scope.unavailable ?? (content.scope.items?.length ? content.scope.items.map((i) => `${i.name}: ${plain(i.state)}`).join("; ") : "No accounts need supporting work.")} <Link to={overview}>Ledger coverage</Link>
        </dd>
        <dt>Monthly hand-over</dt>
        <dd>
          {content.routine
            ? `Each new month is handed to the agent, finished at ${content.routine.finish === "signoff" ? "sign-off" : "ready for review"}${content.routine.due_days ? `, due ${content.routine.due_days} days after month-end` : ""}.`
            : "Not set up."}{" "}
          <Link to={overview}>Hand-over card</Link>
        </dd>
      </dl>
      {latest && latest.digest === view.digest && !editing ? (
        <p role="status">
          Confirmed by {latest.confirmed_by} on {dates.dateTime(latest.confirmed_at)}; the client's timezone is {latest.timezone}.{" "}
          <button type="button" onClick={() => setEditing(true)}>Change the timezone</button>
        </p>
      ) : (
        <form onSubmit={(e) => void confirm(e)}>
          <fieldset disabled={busy}>
            {latest && <p role="status">The setup changed since it was confirmed on {dates.date(latest.confirmed_at)}. This month stays confirmed; confirm again to record the change.</p>}
            {view.problems?.map((p) => <p role="alert" key={p}>{p}</p>)}
            <label>
              Client's timezone
              <input name="timezone" required list="takeover-zones" defaultValue={latest?.timezone ?? device} key={latest?.timezone ?? device} />
              <datalist id="takeover-zones">{zones.map((z) => <option key={z} value={z} />)}</datalist>
            </label>
            <label>
              <input type="checkbox" name="confirmed" required /> This is the company, basis, procedures, access, scope and hand-over this client is taken over with.
            </label>
            <button className="primary" disabled={(view.problems?.length ?? 0) > 0}>Confirm the takeover setup</button>
          </fieldset>
        </form>
      )}
    </section>
  );
}
