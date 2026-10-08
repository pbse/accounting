import { useEffect, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router";
import { PageHeader } from "@close/ui/components/PageHeader";
import { VerticalTabs } from "@close/ui/components/VerticalTabs";
import { authKitClientId, useSession } from "../auth/Auth";
import { AgentAccess } from "../closes/AgentAccess";
import { kindLabels } from "../closes/Attention";
import { workspaceRequest } from "../closes/workspace";

type Me = { subject: string; time_zone: string; notifications: { kinds: string[]; browser: boolean; all_kinds: string[] } };
// What the sign-in provider keeps, read apart from the settings kept here so a slow provider never hides those.
type Profile = { email?: string; first_name?: string; last_name?: string; name_editable: boolean; profile_unavailable?: boolean };
type Drive = { configured: boolean; connected?: boolean; reason?: string };

// One read of a settings route, kept until it is replaced; each provider-backed part loads on its own.
function useRead<T>(path: string, unavailable: string) {
  const [value, setValue] = useState<T>();
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    workspaceRequest(path)
      .then((data) => live && setValue(data))
      .catch((e) => live && setError(e instanceof Error ? e.message : unavailable));
    return () => {
      live = false;
    };
  }, [path, unavailable]);
  return { value, setValue, error };
}

const tabs = [
  { key: "general", label: "General", to: "/dashboard/settings", icon: "owner" },
  { key: "connections", label: "Connections", to: "/dashboard/settings?tab=connections", icon: "link" },
  { key: "notifications", label: "Notifications", to: "/dashboard/settings?tab=notifications", icon: "decision" },
] as const;

const deviceZone = () => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone ?? "";
  } catch {
    return "";
  }
};
const zones = (): string[] => {
  try {
    return "supportedValuesOf" in Intl ? (Intl as unknown as { supportedValuesOf: (k: string) => string[] }).supportedValuesOf("timeZone") : [];
  } catch {
    return [];
  }
};

// Who the person is and where they are; the name is the sign-in provider's, the time zone this firm's record of them.
function General({ me, onSaved }: { me: Me; onSaved: () => void }) {
  // Local development has no hosted session to read; the hook is chosen once, as the layout does.
  const session = authKitClientId ? useSession() : undefined;
  const { value: profile, setValue: setProfile, error: profileError } = useRead<Profile>("/me/profile", "Your sign-in profile could not be read.");
  const editable = profile?.name_editable ?? false;
  const [first, setFirst] = useState("");
  const [last, setLast] = useState("");
  const [zone, setZone] = useState(me.time_zone);
  useEffect(() => {
    setFirst(profile?.first_name ?? "");
    setLast(profile?.last_name ?? "");
  }, [profile]);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setMessage("");
    try {
      const changed = { time_zone: zone, ...(editable && (first !== (profile?.first_name ?? "") || last !== (profile?.last_name ?? "")) ? { first_name: first, last_name: last } : {}) };
      const saved: { first_name: string | null; last_name: string | null } = await workspaceRequest("/me", changed);
      // The saved name is the new baseline, so a later change sends only what differs from it.
      if (saved.first_name !== null && profile) setProfile({ ...profile, first_name: saved.first_name, last_name: saved.last_name ?? "" });
      setMessage("Saved.");
      onSaved();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Your settings could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="General">
      <h2>General</h2>
      <p>Signed in as {profile?.email || session?.user?.email || "the local development identity"}. Your email and sign-in belong to your firm's sign-in provider.</p>
      <form onSubmit={save}>
        <div className="form-grid">
          <label>
            First name
            <input value={first} maxLength={100} disabled={!editable} onChange={(e) => setFirst(e.target.value)} />
          </label>
          <label>
            Last name
            <input value={last} maxLength={100} disabled={!editable} onChange={(e) => setLast(e.target.value)} />
          </label>
          <label>
            Time zone
            <input list="settings-time-zones" value={zone} maxLength={64} placeholder={deviceZone() || "Region/City"} autoComplete="off" onChange={(e) => setZone(e.target.value)} />
            <small>By region and city, such as America/New_York. Kept for this firm; the workspace still shows times in this device's zone.</small>
          </label>
        </div>
        <datalist id="settings-time-zones">
          {zones().map((z) => (
            <option key={z} value={z} />
          ))}
        </datalist>
        {profileError || profile?.profile_unavailable ? (
          <p role="alert">Your name could not be read from the sign-in provider just now. The rest of your settings are here; reload to try the name again.</p>
        ) : !profile ? (
          <p role="status">Reading your name…</p>
        ) : (
          !editable && <p className="note">Your name is kept by the sign-in provider, which local development does not have.</p>
        )}
        <p>
          <button className="primary" disabled={busy}>Save</button>{" "}
          {deviceZone() && deviceZone() !== zone && (
            <button type="button" disabled={busy} onClick={() => setZone(deviceZone())}>
              Use this device's time zone
            </button>
          )}
        </p>
        {message && <p role="status">{message}</p>}
      </form>
    </section>
  );
}

// What is connected for this person alone. Ledgers, senders and posting authority are the firm's and are linked, not kept here.
function Connections() {
  const { value: drive, error } = useRead<Drive>("/me/drive", "Google Drive could not be checked.");
  const [message, setMessage] = useState("");
  async function connect() {
    setMessage("");
    try {
      window.location.assign((await workspaceRequest("/me/drive/connect", {})).url);
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Google Drive connection is unavailable.");
    }
  }
  return (
    <>
      <AgentAccess />
      <section aria-label="Google Drive">
        <h2>Google Drive</h2>
        <p>Search your own Drive for a PDF, text or CSV file from a close's evidence pane. Only you can see your Drive; the grant is yours, through your firm's WorkOS connection.</p>
        {error ? (
          <p role="alert">{error}</p>
        ) : !drive ? (
          <p role="status">Checking your Drive connection…</p>
        ) : !drive.configured ? (
          <p className="note">Google Drive needs the firm's hosted sign-in, which local development does not have.</p>
        ) : drive.connected ? (
          <p>Connected.</p>
        ) : (
          <p>
            {drive.reason}{" "}
            <button type="button" onClick={() => void connect()}>
              Connect Google Drive
            </button>
          </p>
        )}
        {message && <p role="alert">{message}</p>}
      </section>
      <section aria-label="Your firm's connections">
        <h2>Your firm's connections</h2>
        <p>
          A client's QuickBooks Online or Xero connection belongs to the client's books and is made from that client's close, under its Settings tab, by a member the client's grant names. The firm's Stripe reports and approved email sender are in{" "}
          <Link to="/dashboard/firm">Firm settings</Link>; every connected service, posting grant and what each outside service receives is listed in the <Link to="/dashboard/security">Security center</Link>.
        </p>
        <p>
          Nothing posts to a ledger on a personal setting. A person previews, approves and executes each posting under the firm's policy and the client's grant; a bounded standing approval on a workpaper is the only way a recurring entry goes through without a fresh approval, and the firm, not a member, grants it.
        </p>
      </section>
    </>
  );
}

// Which attention events count toward the person's unread badge, and whether this browser may say when new ones arrive.
function Notifications({ me, onSaved }: { me: Me; onSaved: () => void }) {
  const [kinds, setKinds] = useState(me.notifications.kinds);
  const [browser, setBrowser] = useState(me.notifications.browser);
  const [message, setMessage] = useState("");
  async function save(e: FormEvent) {
    e.preventDefault();
    try {
      if (browser && "Notification" in window && Notification.permission === "default") await Notification.requestPermission();
      await workspaceRequest("/attention/preferences", { kinds, browser });
      setMessage(browser && (!("Notification" in window) || Notification.permission !== "granted") ? "Saved. This browser blocks notifications, so only the badge shows new items." : "Saved.");
      onSaved();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Preferences could not be saved.");
    }
  }
  return (
    <section aria-label="Notifications">
      <h2>Notifications</h2>
      <form onSubmit={save}>
        <fieldset>
          <legend>Counted in your unread badge and notifications</legend>
          {me.notifications.all_kinds.map((k) => (
            <label key={k} className="checkbox-label">
              <input type="checkbox" checked={kinds.includes(k)} onChange={(e) => setKinds(e.target.checked ? [...kinds, k] : kinds.filter((x) => x !== k))} /> {kindLabels[k] ?? k}
            </label>
          ))}
        </fieldset>
        <label className="checkbox-label">
          <input type="checkbox" checked={browser} onChange={(e) => setBrowser(e.target.checked)} />
          <span>
            Show a browser notification for new items while the workspace is open. It says only how many items are new, never a client, amount or document, and the page it opens
            reads them with your current access.
          </span>
        </label>
        <button className="primary">Save preferences</button>
        {message && <p role="status">{message}</p>}
      </form>
    </section>
  );
}

// A member's own settings: who they are, what is connected for them alone, and what notifies them. Firm policy stays in Firm settings.
export default function Settings() {
  const [params] = useSearchParams();
  const tab = tabs.find((t) => t.key === params.get("tab"))?.key ?? "general";
  const [me, setMe] = useState<Me>();
  const [error, setError] = useState("");
  const [version, setVersion] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest("/me")
      .then((data) => live && setMe(data))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Your settings are unavailable."));
    return () => {
      live = false;
    };
  }, [version]);
  const refresh = () => setVersion((v) => v + 1);
  return (
    <>
      {/* Keyed by section so each section counts as its own rendered page (scripts/layout-check.mjs). */}
      <PageHeader key={tab} icon="settings" title="Your settings">
        Your name and time zone, what is connected for you alone, and what notifies you. The firm's policies and connections are in Firm settings.
      </PageHeader>
      <VerticalTabs label="Settings sections" items={[...tabs]} value={tab}>
        {error ? (
          <p role="alert">{error}</p>
        ) : !me ? (
          <p role="status">Opening…</p>
        ) : tab === "connections" ? (
          <Connections />
        ) : tab === "notifications" ? (
          <Notifications me={me} onSaved={refresh} />
        ) : (
          <General me={me} onSaved={refresh} />
        )}
      </VerticalTabs>
    </>
  );
}
