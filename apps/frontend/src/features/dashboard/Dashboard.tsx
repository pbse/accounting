import { Work } from "../closes/Work";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { referenceWorkpaperUrl } from "../../lib/api";
import type { ReferenceWorkpaper } from "../../../../../packages/contracts/reference";
import { Review } from "../review/Review";
import { PageHeader } from "@close/ui/components/PageHeader";

export default function Dashboard({
  view,
}: {
  view: "work" | "close" | "review";
}) {
  const [workpaper, setWorkpaper] = useState<ReferenceWorkpaper>();
  const [error, setError] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (view === "work") return;
    const controller = new AbortController();
    setError(false);
    fetch(referenceWorkpaperUrl, { signal: controller.signal })
      .then((response) => {
        if (!response.ok) throw new Error("Unavailable");
        return response.json();
      })
      .then(setWorkpaper)
      .catch(() => {
        if (!controller.signal.aborted) setError(true);
      });
    return () => controller.abort();
  }, [attempt, view]);

  if (view === "work") return <Work />;

  return (
    <>
      <PageHeader
        icon="book"
        title={view === "close" ? "Example close: Acme Services · September" : "Example review"}
        actions={<span className="pill neutral">Synthetic reference · September 2026</span>}
      >
        Preparation, approval, verified posting, and firm sign-off remain separate.
      </PageHeader>
      {error ? (
        <section role="alert">
          <h2>We couldn’t load the reference workpaper.</h2>
          <p>
            Check that the API is running, then try again. Your work hasn’t been
            changed.
          </p>
          <button className="primary" onClick={() => setAttempt(attempt + 1)}>
            Try again
          </button>
        </section>
      ) : !workpaper ? (
        <p role="status">Loading the reference workpaper…</p>
      ) : (
        <>
          <div className="context">
            <strong>{workpaper.entity}</strong>
            <span>{workpaper.period}</span>
            <span>
              {workpaper.basis} · {workpaper.currency}
            </span>
            <span>Preparation only</span>
          </div>
          {view === "close" ? (
            <section>
              <h2>What this example close includes</h2>
              <dl>
                <dt>Prepaids</dt>
                <dd>Synthetic reference schedule; no customer approval.</dd>
                <dt>Statement reconciliation</dt>
                <dd>
                  Blocked: independent statement and ledger detail required.
                </dd>
                <dt>Posting</dt>
                <dd>Not enabled. No entry has been sent to a ledger.</dd>
                <dt>Firm sign-off</dt>
                <dd>Not signed off.</dd>
              </dl>
              <Link className="button primary" to="/dashboard/review">
                Inspect prepared schedule
              </Link>
            </section>
          ) : (
            <Review workpaper={workpaper} />
          )}
        </>
      )}
    </>
  );
}
