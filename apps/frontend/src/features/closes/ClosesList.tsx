import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { authKitClientId } from "../auth/Auth";
import { Icon } from "@close/ui/components/Icon";
import { PageHeader } from "@close/ui/components/PageHeader";
import { AskAgent } from "../dashboard/AskAgent";
import { starters } from "../dashboard/assistantStore";
import { EntityCell, RowList } from "@close/ui/components/RowList";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import { FirmEnrollment } from "./Enterprise";
import { PlanChips } from "./Planning";
import { workspaceRequest } from "./workspace";
import type { Close } from "./WorkpaperView";

export function isEnrollmentError(message: string) {
  return !!authKitClientId && message.includes("firm or membership");
}

export default function ClosesList() {
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const query = params.get("q") ?? "";
  const [items, setItems] = useState<Close[]>([]);
  const [more, setMore] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let live = true;
    setLoading(true);
    setError("");
    workspaceRequest(query ? `?q=${encodeURIComponent(query)}` : "")
      .then((data) => {
        if (!live) return;
        setItems(data.closes ?? []);
        setMore(data.has_more);
      })
      .catch((e) => live && setError(e instanceof Error ? e.message : "Could not open client closes."))
      .finally(() => live && setLoading(false));
    return () => {
      live = false;
    };
  }, [query, refresh]);
  async function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    setBusy(true);
    setError("");
    try {
      const saved = await workspaceRequest("", { entity: data.get("entity"), period: data.get("period") });
      navigate(`/dashboard/closes/${saved.id}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed. Retry the same client and month safely.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <PageHeader icon="clients" title="Client closes">
        One place per client and month for evidence, prepared work, requests and sign-off.
      </PageHeader>
      <AskAgent
        description="Ask which closes need you, are ready for review or are blocked: the agent answers from your workspace and shows anything it would do for you to confirm."
        placeholder="Ask about your clients' closes, such as “what is ready for review?”"
        suggestions={starters.firm}
      />
      {error && <p role="alert">{error}</p>}
      {isEnrollmentError(error) && <FirmEnrollment onEnrolled={() => setRefresh((n) => n + 1)} />}
      <section>
        <h2>Open or create a close</h2>
        <form onSubmit={create}>
          <fieldset disabled={busy}>
            <div className="form-grid">
              <label>
                Client / entity
                <input name="entity" required maxLength={200} defaultValue={query} />
              </label>
              <label>
                Close month
                <input name="period" type="month" required />
              </label>
            </div>
            <button className="primary">Create or open close</button>
          </fieldset>
        </form>
      </section>
      <SectionHeader title={query ? `Closes matching “${query}”` : "Recent closes"}>
        {query && (
          <button type="button" onClick={() => setParams({})}>
            Clear search
          </button>
        )}
      </SectionHeader>
      <RowList columns={["Client", "Close month", "Plan", ""]}>
        {loading && <p role="status" className="empty">Loading closes…</p>}
        {!loading && items.length === 0 && <p className="empty">{query ? "No closes match this search." : "No closes yet. Create your first client close above."}</p>}
        {items.map((item) => (
          <Link className="row" key={item.id} to={`/dashboard/closes/${item.id}`}>
            <EntityCell name={item.entity} title={item.entity} />
            <span className="chip">{item.period}</span>
            <span>
              <PlanChips plan={item} />
            </span>
            <Icon name="arrow" />
          </Link>
        ))}
      </RowList>
      {more && <p className="note">Showing the 100 most recent matches. Search by client or YYYY-MM to narrow the list.</p>}
    </>
  );
}
