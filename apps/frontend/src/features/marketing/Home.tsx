import { Link } from "react-router";

export function Home() {
  return (
    <main id="main" className="public-page">
      <div className="hero">
        <div>
          <p className="eyebrow">
            THE CLOSE PREPARATION WORKSPACE · IN DEVELOPMENT
          </p>
          <h1>
            Your clients’ books.
            <br />
            <em>Prepared for your review.</em>
          </h1>
          <p className="hero-copy">
            An AI preparation team built around the way accounting firms work.
            Bring the evidence, calculations, and decisions together—so your
            next close starts with context.
          </p>
          <div className="hero-actions">
            <Link className="button primary" to="/contact">
              Let’s talk about your firm <span aria-hidden="true">→</span>
            </Link>
            <Link className="text-link" to="/dashboard">
              Explore the example workspace
            </Link>
          </div>
          <p className="source-label">
            You keep your ledger. You make the accounting judgments.
          </p>
        </div>
        <aside
          className="hero-workpaper"
          aria-label="Illustrative workpaper preview"
        >
          <div className="preview-heading">
            <span>YOUR NEXT CLOSE</span>
            <span className="pill">Illustrative example</span>
          </div>
          <h2>
            One schedule.
            <br />
            The whole picture.
          </h2>
          <div className="preview-row">
            <span>01</span>
            <div>
              <strong>The source</strong>
              <p>Invoice, dates, and ledger entry together.</p>
            </div>
          </div>
          <div className="preview-row">
            <span>02</span>
            <div>
              <strong>The preparation</strong>
              <p>A clear calculation and proposed adjustment.</p>
            </div>
          </div>
          <div className="preview-row">
            <span>03</span>
            <div>
              <strong>Your decision</strong>
              <p>Review the evidence before anything is posted.</p>
            </div>
          </div>
          <Link to="/dashboard/review">
            Take a look at the workpaper <span aria-hidden="true">↗</span>
          </Link>
        </aside>
      </div>
      <div className="marketing-intro">
        <p className="eyebrow">BUILT FOR THE WORK BETWEEN THE NUMBERS</p>
        <h2>
          Less rebuilding context.
          <br />
          More room for judgment.
        </h2>
        <p>
          We’re building Close for bookkeeping and client accounting services
          firms, starting with ordinary US service businesses and supported
          month-end work.
        </p>
      </div>
      <div className="benefits">
        <section>
          <span className="eyebrow">01 / PREPARE</span>
          <h3>Work that moves forward</h3>
          <p>
            The goal is a team that gathers permitted evidence, prepares
            supported work, and returns with specific decisions—not another list
            of tasks.
          </p>
        </section>
        <section>
          <span className="eyebrow">02 / REVIEW</span>
          <h3>Evidence within reach</h3>
          <p>
            Inspect the source, accounting method, calculation, and proposed
            effect in one place. Missing information stays visible.
          </p>
        </section>
        <section>
          <span className="eyebrow">03 / STAY IN CONTROL</span>
          <h3>Your firm has the final word</h3>
          <p>
            Preparation, approval, posting, and sign-off are separate steps. The
            connected accounting platform remains your ledger of record.
          </p>
        </section>
      </div>
      <section className="marketing-cta">
        <div>
          <p className="eyebrow">START WITH YOUR NEXT CLOSE</p>
          <h2>Make space for the work only you can do.</h2>
          <p>Tell us how your firm prepares and reviews its client work.</p>
        </div>
        <Link className="button primary" to="/contact">
          Reach out to us →
        </Link>
      </section>
    </main>
  );
}
