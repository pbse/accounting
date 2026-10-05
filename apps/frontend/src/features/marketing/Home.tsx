import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { ILLUSTRATIVE_DECISIONS, startCity, type City } from "./city";
import { startAgents } from "./agents";
import { prefersReducedMotion } from "./noise";
import { WORKPAPERS } from "./workpapers";

export function Home() {
  return (
    <main id="main">
      <Hero />
      <AgentsAtWork />
      <section className="k-col k-start" id="start" aria-labelledby="start-title">
        <h2 id="start-title">See one of your closes, prepared.</h2>
        <p className="k-lede">Tell us how your firm works. We’ll show you what the agents prepare, and what they leave for you.</p>
        <div className="k-signhere">
          <span className="k-x" aria-hidden="true">
            ×
          </span>
          <span className="k-sl" aria-hidden="true" />
          <Link className="k-pill k-pill-lg" to="/contact">
            Talk to us <span aria-hidden="true">→</span>
          </Link>
        </div>
      </section>
    </main>
  );
}

// The plate: a month of transactions settles into a skyline, then the work carries on (./city.ts).
function Hero() {
  const hero = useRef<HTMLElement>(null), canvas = useRef<HTMLCanvasElement>(null), copy = useRef<HTMLDivElement>(null);
  const card = useRef<HTMLDivElement>(null), cardClient = useRef<HTMLParagraphElement>(null), cardAsk = useRef<HTMLParagraphElement>(null), cardAmount = useRef<HTMLParagraphElement>(null);
  const tag = useRef<HTMLDivElement>(null), tagTitle = useRef<HTMLSpanElement>(null), tagClient = useRef<HTMLParagraphElement>(null), tagLine = useRef<HTMLParagraphElement>(null);
  const transactions = useRef<HTMLElement>(null), decisions = useRef<HTMLElement>(null);
  const city = useRef<City | null>(null);
  const [paused, setPaused] = useState(false);
  useEffect(() => {
    city.current = startCity({
      hero: hero.current!, canvas: canvas.current!, copy: copy.current!,
      card: card.current!, cardClient: cardClient.current!, cardAsk: cardAsk.current!, cardAmount: cardAmount.current!,
      tag: tag.current!, tagTitle: tagTitle.current!, tagClient: tagClient.current!, tagLine: tagLine.current!,
      transactions: transactions.current!, decisions: decisions.current!,
    });
    return () => city.current?.destroy();
  }, []);
  const toggle = () => {
    city.current?.setPaused(!paused);
    setPaused(!paused);
  };
  return (
    <section className="k-hero" id="top" aria-labelledby="title" ref={hero}>
      <canvas className="k-city" ref={canvas} aria-hidden="true" />
      <div className="k-copy" ref={copy}>
        <p className="k-eyebrow">For accounting firms and finance teams</p>
        <h1 id="title">The work, prepared.</h1>
        <p className="k-sub">Krith prepares the work behind every engagement, starting with the month-end close. Your people review, decide and sign.</p>
        <span className="k-bracket">
          <Link className="k-pill k-pill-lg" to="/contact">
            Talk to us <span aria-hidden="true">→</span>
          </Link>
        </span>
      </div>
      <div className="k-city-space" aria-hidden="true" />
      <div className="k-dcard" ref={card} aria-hidden="true">
        <p className="k-dc-client" ref={cardClient} />
        <p className="k-dc-ask" ref={cardAsk} />
        <p className="k-dc-amt" ref={cardAmount} />
        <p className="k-dc-tag">Waiting for you</p>
      </div>
      <div className="k-worktag" ref={tag} aria-hidden="true">
        <p className="k-wt-title">
          <i className="k-wt-dot" />
          <span ref={tagTitle} />
        </p>
        <p className="k-wt-client" ref={tagClient} />
        <p className="k-wt-line" ref={tagLine} />
      </div>
      <p className="k-tally">
        <span className="k-live">
          <button className="k-live-toggle" type="button" aria-pressed={paused} aria-label={paused ? "Play the animation" : "Pause the animation"} onClick={toggle} hidden={prefersReducedMotion()}>
            <i className="k-live-dot" />
          </button>
          September close
        </span>
        <i>·</i>
        <span>
          <b>38</b> clients
        </span>
        <i>·</i>
        <span>
          <b ref={transactions}>18,412</b> transactions prepared
        </span>
        <i>·</i>
        <span className="k-yours">
          <b ref={decisions}>7</b> decisions for you
        </span>
      </p>
      <ul className="k-sr" aria-label="Illustration: decisions waiting for the firm">
        {ILLUSTRATIVE_DECISIONS.map((d) => (
          <li key={d.client}>
            {d.client}: {d.ask}, {d.amt}
          </li>
        ))}
      </ul>
    </section>
  );
}

const CREW = [
  { agent: "co", mark: "CO", name: "Collector", does: "Gathers statements, ledgers and documents" },
  { agent: "ma", mark: "MA", name: "Matcher", does: "Matches every statement line to the ledger" },
  { agent: "in", mark: "IN", name: "Investigator", does: "Traces each difference to its source" },
  { agent: "pr", mark: "PR", name: "Preparer", does: "Drafts the entries and the workpaper" },
];
const ROW_AGENT = [["CO", "Collector"], ["CO", "Collector"], ["MA", "Matcher"], ["MA", "Matcher"], ["IN", "Investigator"]];

function Cursor({ agent, name }: { agent: string; name: string }) {
  return (
    <div className={agent === "you" ? "k-cursor k-cursor-you" : "k-cursor"} data-cursor={agent} aria-hidden="true">
      <span className="k-bob">
        <svg viewBox="0 0 15 18">
          <path d="M1 1 L1 14.6 L4.7 11.3 L7.3 16.8 L9.7 15.7 L7.2 10.3 L12.3 10.1 Z" fill="currentColor" stroke="#ffffff" strokeWidth="1.2" strokeLinejoin="round" />
        </svg>
        <span className="k-who-tag">{name}</span>
      </span>
    </div>
  );
}

// Agents at work: the workpaper four agents prepare and a person signs (./agents.ts runs it).
function AgentsAtWork() {
  const section = useRef<HTMLElement>(null), pause = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    const agents = startAgents(section.current!, pause.current!);
    return () => agents.destroy();
  }, []);
  const first = WORKPAPERS[0];
  return (
    <section className="k-col k-agents" id="how" aria-labelledby="agents-title" ref={section}>
      <p className="k-label">Agents at work</p>
      <h2 id="agents-title">
        <span>Prepared to the cent.</span>
        <span className="k-yours">Signed by you.</span>
      </h2>
      <p className="k-lede">On every close, a team of agents gathers the evidence, matches every line, traces each difference and drafts the entries. You review their work and sign.</p>
      <div className="k-stage">
        <figure className="k-ledger" aria-label="Illustrative bank reconciliation prepared by agents and signed by a reviewer">
          <div className="k-lhead">
            <span className="k-lhead-title">{first.head}</span>
            <span className="k-status is-signed" aria-live="polite">
              <i />
              <span className="k-status-text">Reviewed and signed</span>
            </span>
          </div>
          {first.rows.map(([label, tag, title, text, amount], i) => (
            <div className="k-row" key={i}>
              <span className="k-mk" title={`${ROW_AGENT[i][1]} agent`}>
                {ROW_AGENT[i][0]}
              </span>
              <span className="k-l">{label}</span>
              <span className="k-src" tabIndex={0}>
                <span className="k-src-tag">{tag}</span>
                <span className="k-card" role="tooltip">
                  <b>{title}</b>
                  <span className="k-card-text">{text}</span>
                </span>
              </span>
              <span className="k-amt">{amount}</span>
            </div>
          ))}
          <div className="k-diff">
            <span className="k-mk" title="Preparer agent">
              PR
            </span>
            <span className="k-l">Difference</span>
            <span className="k-figure">
              <span className="k-odo" role="img" aria-label="Difference 0.00" />
            </span>
          </div>
          <div className="k-dbl is-on" />
          <p className="k-aje">
            <span className="k-mk" title="Preparer agent">
              PR
            </span>
            <span>
              <b className="k-aje-ref">{first.ajeRef} drafted, not posted.</b> <span className="k-aje-text">{first.aje}</span>
            </span>
          </p>
          <div className="k-marks">
            <div className="k-mark">
              <span className="k-key">Prepared</span>
              <span className="k-line">
                <span className="k-who">
                  {CREW.map((c) => (
                    <span className="k-mk" key={c.agent}>
                      {c.mark}
                    </span>
                  ))}
                </span>
              </span>
              <span className="k-meta k-prep-meta">{first.prepared}</span>
            </div>
            <div className="k-mark k-reviewed is-on">
              <span className="k-key">Reviewed</span>
              <span className="k-line">
                <span className="k-sig is-on" aria-hidden="true">
                  {first.signer}
                </span>
              </span>
              <span className="k-meta k-rev-meta">{first.meta}</span>
            </div>
          </div>
          <div className="k-ledger-foot">
            <span>Illustrative clients and figures</span>
            <button className="k-textbtn" type="button" aria-pressed="false" ref={pause}>
              Pause
            </button>
          </div>
        </figure>
        {CREW.map((c) => (
          <Cursor key={c.agent} agent={c.agent} name={c.name} />
        ))}
        <Cursor agent="you" name="You" />
      </div>
      <ul className="k-crew" aria-label="Who prepares and who signs">
        {CREW.map((c) => (
          <li data-agent={c.agent} key={c.agent}>
            <span className="k-mk">{c.mark}</span>
            <b>{c.name}</b>
            <span>{c.does}</span>
          </li>
        ))}
        <li data-agent="you" className="k-crew-you">
          <span className="k-mk">YOU</span>
          <b>You</b>
          <span>Review the work and sign</span>
        </li>
      </ul>
      <div className="k-trust">
        <p>Agents can’t approve an entry, send anything to a client or sign off a close. People do. Client data is never used to train models, and every staff access to it is logged.</p>
        <Link to="/security">How we protect client data →</Link>
      </div>
    </section>
  );
}
