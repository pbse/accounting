import { useEffect, useState } from "react";
import { Link } from "react-router";
import { workspaceRequest } from "../closes/workspace";
import { RequestError } from "../closes/requestError";
import type { ReferenceWorkpaper } from "../../../../../packages/contracts/reference";
import { Review } from "../review/Review";
import { PageHeader } from "@close/ui/components/PageHeader";
import Home from "../home/Home";

export default function Dashboard({
  view,
}: {
  view: "work" | "close" | "review";
}) {
  const [workpaper, setWorkpaper] = useState<ReferenceWorkpaper>();
  const [error, setError] = useState<"failed" | "none">();
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (view === "work") return;
    let live = true;
    setError(undefined);
    // The worked example is Krith's own firm's alone; any other firm is told there is none.
    workspaceRequest("/examples/prepaid")
      .then((paper) => live && setWorkpaper(paper as ReferenceWorkpaper))
      .catch((e) => live && setError(e instanceof RequestError && e.status === 404 ? "none" : "failed"));
    return () => {
      live = false;
    };
  }, [attempt, view]);

  if (view === "work") return <Home />;
  if (error === "none")
    return (
      <section>
        <h1>There is no example here</h1>
        <p>The worked example is only in Krith’s own workspace.</p>
        <Link to="/dashboard">Back to your work</Link>
      </section>
    );

  return (
    <>
      <PageHeader
        icon="book"
        title={view === "close" ? "Example close: Acme Services · September" : "Example review"}
        actions={<span className="pill neutral">Synthetic reference · September 2026</span>}
      >
        Preparation, approval, verified posting, and firm sign-off remain separate.
      </PageHeader>
      {error === "failed" ? (
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
