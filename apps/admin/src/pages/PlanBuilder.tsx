import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { post, useResource } from "../api";
import { ActionForm } from "../components/ActionForm";
import { FlowGraph, type PlanNode } from "../components/Flow";
import { Reveal, useRevealable } from "../components/Reveal";
import { useGrantFor } from "../components/Grants";

type Kind = "activity" | "workflow" | "event" | "sleep" | "approval";
// One step as the form edits it; toPlan turns the list into plan JSON. Steps refer to each other by key, which never
// changes, so renaming a step (even to another step's ID for a moment) never rewires what waits for what.
type Step = {
  key: string;
  id: string;
  kind: Kind;
  activity: string;
  workflow: string;
  input: string; // the JSON as written, never parsed and re-written, so large numbers stay exact
  after: string[]; // keys
  whenKey: string;
  whenOutcome: string;
  event: string;
  timeout: string;
  duration: string;
  permission: string;
  subject: string;
  prompt: string;
};
type Schema = { type?: string; properties?: Record<string, Schema> };
type Activity = { Name: string; Title: string; Description: string; Permission: string; Effect: string; Input: Schema };
type Definition = { name: string; title: string; input_schema: Schema };
type StoredNode = PlanNode & { input?: unknown; approval?: { permission: string; subject?: string; prompt?: string; timeout?: string } };
type Stored = {
  plan: { id: number; organization: string | null; state: string; source: string; is_template: boolean; plan?: { title: string; nodes: StoredNode[] } };
  inputs?: Record<string, string>; // each step's input exactly as stored
  revealed: boolean;
};

// What each kind of step can end with, for a later step's "when".
const outcomes: Record<Kind, string[]> = {
  activity: ["completed", "failed"],
  workflow: ["completed", "failed"],
  event: ["received", "timed_out"],
  sleep: ["completed"],
  approval: ["approved", "rejected", "timed_out"],
};
const kindLabel: Record<Kind, string> = { activity: "Run an activity", workflow: "Run a workflow", event: "Wait for an event", sleep: "Wait a while", approval: "Ask for approval" };

// prune keeps every step consistent after any edit: a step comes after earlier steps only, and its "when" names one of
// them with an outcome that step's kind can have.
function prune(steps: Step[]): Step[] {
  return steps.map((s, i) => {
    const earlier = steps.slice(0, i);
    const after = s.after.filter((a) => earlier.some((e) => e.key === a));
    const from = earlier.find((e) => e.key === s.whenKey);
    const keep = from && after.includes(s.whenKey) && outcomes[from.kind].includes(s.whenOutcome);
    return { ...s, after, whenKey: keep ? s.whenKey : "", whenOutcome: keep ? s.whenOutcome : "" };
  });
}

// unusedID is the first step_<n> no step has.
function unusedID(steps: Step[]): string {
  let n = steps.length + 1;
  while (steps.some((s) => s.id === `step_${n}`)) n++;
  return `step_${n}`;
}

const blank = (id: string): Step => ({
  key: crypto.randomUUID(), id, kind: "activity", activity: "", workflow: "", input: "{}", after: [], whenKey: "", whenOutcome: "",
  event: "", timeout: "72h", duration: "24h", permission: "", subject: "", prompt: "",
});

// A starting input for a schema: each field with an empty value of its type.
function skeleton(schema?: Schema): string {
  const out: Record<string, unknown> = {};
  for (const [name, field] of Object.entries(schema?.properties ?? {})) {
    out[name] = field.type === "string" ? "" : field.type === "integer" || field.type === "number" ? 0 : field.type === "boolean" ? false : field.type === "array" ? [] : field.type === "object" ? {} : null;
  }
  return JSON.stringify(out, null, 2);
}

function fromPlan(nodes: StoredNode[], inputs: Record<string, string> = {}): Step[] {
  const steps = nodes.map((n) => blank(n.id));
  const keyOf = (id: string) => steps.find((s) => s.id === id)?.key ?? "";
  return nodes.map((n, i) => {
    const s = steps[i];
    s.after = (n.after ?? []).map(keyOf).filter(Boolean);
    if (n.when) {
      const [on, outcome] = n.when.split(".");
      s.whenKey = keyOf(on);
      s.whenOutcome = outcome;
    }
    s.input = inputs[n.id] ?? (n.input === undefined ? "{}" : JSON.stringify(n.input, null, 2));
    if (n.activity) Object.assign(s, { kind: "activity", activity: n.activity });
    else if (n.workflow) Object.assign(s, { kind: "workflow", workflow: n.workflow });
    else if (n.wait?.event) Object.assign(s, { kind: "event", event: n.wait.event, timeout: n.wait.timeout ?? "" });
    else if (n.wait) Object.assign(s, { kind: "sleep", duration: n.wait.duration ?? "" });
    else if (n.approval) Object.assign(s, { kind: "approval", permission: n.approval.permission, subject: n.approval.subject ?? "", prompt: n.approval.prompt ?? "", timeout: n.approval.timeout ?? "" });
    return s;
  });
}

// toPlan builds the plan as JSON text, with each input spliced in exactly as written (parsing would round integers
// beyond 2^53), plus a parsed copy for the preview; or says which step's input is not JSON.
function toPlan(title: string, steps: Step[]): { text?: string; preview?: StoredNode[]; error?: string } {
  const idOf = (key: string) => steps.find((s) => s.key === key)?.id ?? "";
  const nodes: StoredNode[] = [];
  const texts: string[] = [];
  for (const s of steps) {
    const n: StoredNode = { id: s.id };
    if (s.after.length) n.after = s.after.map(idOf);
    if (s.whenKey) n.when = `${idOf(s.whenKey)}.${s.whenOutcome}`;
    if (s.kind === "activity" || s.kind === "workflow") {
      if (s.kind === "activity") n.activity = s.activity;
      else n.workflow = s.workflow;
      const raw = (s.input || "{}").trim();
      try {
        n.input = JSON.parse(raw); // for the preview only
      } catch {
        return { error: `Step “${s.id}”: the input is not valid JSON.` };
      }
      // The node without its input, then the input appended as written.
      const { input: _, ...rest } = n;
      texts.push(JSON.stringify(rest).replace(/}$/, `,"input":${raw}}`));
    } else {
      if (s.kind === "event") n.wait = { event: s.event, timeout: s.timeout };
      else if (s.kind === "sleep") n.wait = { duration: s.duration };
      else n.approval = { permission: s.permission, ...(s.subject && { subject: s.subject }), ...(s.prompt && { prompt: s.prompt }), timeout: s.timeout };
      texts.push(JSON.stringify(n));
    }
    nodes.push(n);
  }
  return { text: `{"version":1,"title":${JSON.stringify(title)},"nodes":[${texts.join(",")}]}`, preview: nodes };
}

// withPlan is a request body: the fields, and the plan's JSON text as it is.
const withPlan = (fields: Record<string, unknown>, text: string) => JSON.stringify(fields).replace(/}$/, `${Object.keys(fields).length ? "," : ""}"plan":${text}}`);

// New plan or template (/plans/new, optionally ?template=1, ?firm= or ?from=<plan> to start from a copy), or an edit
// of a draft (/plans/:id/edit). Editing or copying reads a plan's content, so it is revealed first.
export function PlanBuilder() {
  const { id } = useParams();
  const [params] = useSearchParams();
  const source = id ?? params.get("from") ?? "";
  return <Builder key={`${id ?? "new"}-${source}`} editing={id} source={source} template={params.get("template") === "1"} firm={params.get("firm") ?? ""} />;
}

function Builder({ editing, source, template: startAsTemplate, firm: startFirm }: { editing?: string; source: string; template: boolean; firm: string }) {
  const navigate = useNavigate();
  const [title, setTitle] = useState("");
  const [steps, setSteps] = useState<Step[]>([blank("first")]);
  const [firm, setFirm] = useState(startFirm);
  const [template, setTemplate] = useState(startAsTemplate);
  const [loaded, setLoaded] = useState(!source);
  // Loaded under a content grant: the builder holds that content only while the grant does.
  const [underGrant, setUnderGrant] = useState(false);
  const [lapsed, setLapsed] = useState(false);
  const stored = useRevealable<Stored>(source ? `/v1/admin/plans/${encodeURIComponent(source)}` : null);
  const grant = useGrantFor(stored.data?.plan.organization);
  const activities = useResource<{ activities: Activity[] }>("/v1/admin/workflows/activities");
  const definitions = useResource<{ workflows: Definition[] }>("/v1/admin/workflows/definitions");

  // Fill the form once from the revealed plan being edited or copied.
  useEffect(() => {
    if (loaded || !stored.data?.revealed || !stored.data.plan.plan) return;
    const p = stored.data.plan;
    setTitle(editing ? p.plan!.title : `Copy of ${p.plan!.title}`);
    setSteps(fromPlan(p.plan!.nodes, stored.data.inputs));
    setFirm(p.organization ?? "");
    if (editing) setTemplate(p.is_template);
    setUnderGrant(stored.reveal.reason === "");
    setLoaded(true);
  }, [loaded, stored.data, editing]);
  // A grant that ended or expired takes back the content it loaded, unsaved edits included.
  useEffect(() => {
    if (!underGrant || grant) return;
    setTitle("");
    setSteps([blank("first")]);
    setUnderGrant(false);
    setLoaded(false);
    setLapsed(true);
    stored.reveal.hide();
  }, [underGrant, grant]); // stored.reveal.hide is new each render

  const built = useMemo(() => toPlan(title, steps), [title, steps]);
  const [check, setCheck] = useState<{ problems: string[]; staff_approvable: boolean; digest: string }>();
  // Check the plan as it is written, a moment after the last change.
  useEffect(() => {
    if (!loaded || !built.text) return;
    let current = true;
    const timer = setTimeout(() => {
      post<{ problems: string[]; staff_approvable: boolean; digest: string }>("/v1/admin/plans/validate", withPlan({}, built.text!)).then((c) => current && setCheck(c), () => current && setCheck(undefined));
    }, 400);
    return () => {
      current = false;
      clearTimeout(timer);
    };
  }, [built, loaded]);

  if (source && !loaded) {
    return (
      <>
        <p><Link to={`/plans/${source}`}>← Plan #{source}</Link></p>
        <div className="page-title"><div><div><h1>{editing ? `Edit plan #${source}` : `Copy plan #${source}`}</h1><p>Editing or copying a plan means reading it: reveal it first.</p></div></div></div>
        {stored.error && <p role="alert">{stored.error}</p>}
        {lapsed && <p role="alert">Your grant for this firm ended, so the plan was closed. Reveal it again to go on.</p>}
        <Reveal reveal={stored.reveal} firm={stored.data?.plan.organization} />
      </>
    );
  }
  if (editing && stored.data && (stored.data.plan.source !== "staff" || !["draft", "rejected"].includes(stored.data.plan.state))) {
    return <p role="alert">Only a draft or rejected plan staff wrote can be edited. <Link to={`/plans/new?from=${editing}`}>Copy it into a new plan</Link> instead.</p>;
  }

  const update = (i: number, change: Partial<Step>) => setSteps(prune(steps.map((s, j) => (j === i ? { ...s, ...change } : s))));
  const move = (i: number, by: number) => {
    const next = [...steps];
    [next[i], next[i + by]] = [next[i + by], next[i]];
    setSteps(prune(next));
  };
  const workflows = (definitions.data?.workflows ?? []).filter((w) => w.name !== "plan");
  return (
    <>
      <p><Link to={editing ? `/plans/${editing}` : "/plans"}>← {editing ? `Plan #${editing}` : "Plans"}</Link></p>
      <div className="page-title"><div><div><h1>{editing ? `Edit plan #${editing}` : template ? "New template" : "New plan"}</h1><p>Steps run in order; a step waits for the steps it comes after, and a “when” runs it only on that outcome.</p></div></div></div>
      <div className="run-layout">
        <section>
          <label className="stack">Title<input value={title} maxLength={200} onChange={(event) => setTitle(event.target.value)} placeholder="What this plan does" /></label>
          {steps.map((s, i) => {
            const earlier = steps.slice(0, i);
            const whenOptions = s.after.flatMap((a) => {
              const from = steps.find((x) => x.key === a);
              return from ? outcomes[from.kind].map((o) => ({ value: `${a}|${o}`, label: `${from.id} ends ${o.replaceAll("_", " ")}` })) : [];
            });
            const whenStep = steps.find((x) => x.key === s.whenKey);
            const activity = activities.data?.activities.find((a) => a.Name === s.activity);
            return (
              <fieldset key={s.key} className="schema-form">
                <legend>Step {i + 1}</legend>
                <div className="filters">
                  <label>ID<input value={s.id} onChange={(event) => update(i, { id: event.target.value.trim() })} pattern="[a-z][a-z0-9_]*" /></label>
                  <label>Does
                    <select value={s.kind} onChange={(event) => update(i, { kind: event.target.value as Kind })}>
                      {(Object.keys(kindLabel) as Kind[]).map((k) => <option key={k} value={k}>{kindLabel[k]}</option>)}
                    </select>
                  </label>
                  {/* A step never moves across a step it waits for, or that waits for it: that would drop the dependency. */}
                  <button type="button" disabled={i === 0 || s.after.includes(steps[i - 1].key)} title={i > 0 && s.after.includes(steps[i - 1].key) ? `Comes after ${steps[i - 1].id}: untick that first` : undefined} onClick={() => move(i, -1)}>Up</button>
                  <button type="button" disabled={i === steps.length - 1 || steps[i + 1].after.includes(s.key)} title={i < steps.length - 1 && steps[i + 1].after.includes(s.key) ? `${steps[i + 1].id} comes after this: untick that first` : undefined} onClick={() => move(i, 1)}>Down</button>
                  <button type="button" disabled={steps.length === 1} onClick={() => setSteps(prune(steps.filter((_, j) => j !== i)))}>Remove</button>
                </div>
                {s.kind === "activity" && (
                  <>
                    <label>Activity
                      <select value={s.activity} onChange={(event) => { const a = activities.data?.activities.find((x) => x.Name === event.target.value); update(i, { activity: event.target.value, input: skeleton(a?.Input) }); }}>
                        <option value="">Choose…</option>
                        {activities.data?.activities.map((a) => <option key={a.Name} value={a.Name}>{a.Title} ({a.Name})</option>)}
                      </select>
                    </label>
                    {activity && <small>{activity.Description}{activity.Effect === "external" && " Acts outside our records, so it must come right after an approval step."}{activity.Permission && ` Approving it needs ${activity.Permission} in the firm.`}</small>}
                  </>
                )}
                {s.kind === "workflow" && (
                  <label>Workflow
                    <select value={s.workflow} onChange={(event) => { const w = workflows.find((x) => x.name === event.target.value); update(i, { workflow: event.target.value, input: skeleton(w?.input_schema) }); }}>
                      <option value="">Choose…</option>
                      {workflows.map((w) => <option key={w.name} value={w.name}>{w.title} ({w.name})</option>)}
                    </select>
                  </label>
                )}
                {(s.kind === "activity" || s.kind === "workflow") && (
                  <label>Input (JSON; a text value may use {"{{step.field}}"} to use an earlier step's result)
                    <textarea rows={4} value={s.input} onChange={(event) => update(i, { input: event.target.value })} />
                  </label>
                )}
                {s.kind === "event" && (
                  <div className="filters">
                    <label>Event key<input value={s.event} onChange={(event) => update(i, { event: event.target.value })} placeholder="client_request.replied:{{ask.id}}" /></label>
                    <label>Give up after<input value={s.timeout} onChange={(event) => update(i, { timeout: event.target.value })} placeholder="72h" /></label>
                  </div>
                )}
                {s.kind === "sleep" && <label>Wait for<input value={s.duration} onChange={(event) => update(i, { duration: event.target.value })} placeholder="24h" /></label>}
                {s.kind === "approval" && (
                  <>
                    <div className="filters">
                      <label>Who decides (a firm permission)<input value={s.permission} onChange={(event) => update(i, { permission: event.target.value })} placeholder="closes:signoff" /></label>
                      <label>Decide within<input value={s.timeout} onChange={(event) => update(i, { timeout: event.target.value })} placeholder="72h" /></label>
                    </div>
                    <label>Subject<input value={s.subject} onChange={(event) => update(i, { subject: event.target.value })} /></label>
                    <label>What they are asked<textarea rows={2} value={s.prompt} onChange={(event) => update(i, { prompt: event.target.value })} /></label>
                  </>
                )}
                {earlier.length > 0 && (
                  <div className="filters">
                    <fieldset>
                      <legend>After</legend>
                      {earlier.map((e) => (
                        <label key={e.key} className="check">
                          <input type="checkbox" checked={s.after.includes(e.key)} disabled={!!s.whenKey} onChange={(event) => update(i, { after: event.target.checked ? [...s.after, e.key] : s.after.filter((a) => a !== e.key) })} />
                          {e.id}
                        </label>
                      ))}
                    </fieldset>
                    <label>When
                      {/* The engine checks only the step a "when" names, so the step then waits for that one step alone. */}
                      <select value={s.whenKey ? `${s.whenKey}|${s.whenOutcome}` : ""} onChange={(event) => {
                        const [whenKey, whenOutcome] = event.target.value ? event.target.value.split("|") : ["", ""];
                        update(i, { whenKey, whenOutcome, ...(whenKey && { after: [whenKey] }) });
                      }}>
                        <option value="">They all succeed</option>
                        {whenOptions.map((w) => <option key={w.value} value={w.value}>{w.label}</option>)}
                      </select>
                      {whenStep && <small>Runs only when {whenStep.id} ends this way, so it waits for that step alone.</small>}
                    </label>
                  </div>
                )}
              </fieldset>
            );
          })}
          <p><button type="button" onClick={() => setSteps([...steps, { ...blank(unusedID(steps)), after: steps.length ? [steps[steps.length - 1].key] : [] }])}>Add a step</button></p>
        </section>
        <aside className="inspector">
          <h2>Check</h2>
          {built.error ? <p role="alert">{built.error}</p> : !check ? <p role="status">Checking…</p> : check.problems.length > 0 ? (
            <ul>{check.problems.map((p, i) => <li key={i}>{p}</li>)}</ul>
          ) : (
            <p>
              No problems.{" "}
              {template ? "A template is copied into plans, never approved itself."
                : !firm.trim() ? "Name the firm it runs for."
                : check.staff_approvable ? "Once saved and revealed, staff can approve it." : "A step needs a firm permission, so the firm approves it, not staff."}
            </p>
          )}
          <h2>Save</h2>
          {underGrant && grant && <p className="note">Loaded under your content grant, which ends at {new Date(grant.expires_at).toLocaleTimeString()}: save before then, or the plan closes and unsaved changes are lost.</p>}
          {!editing && (
            <>
              <label className="check"><input type="checkbox" checked={template} onChange={(event) => setTemplate(event.target.checked)} /> A template (copied into plans; never runs itself)</label>
              <label className="stack">Firm ID{template && " (empty: shared by every firm)"}<input value={firm} onChange={(event) => setFirm(event.target.value)} placeholder="org_…" /></label>
            </>
          )}
          <ActionForm label={editing ? "Save changes" : "Save as a draft"} minLength={10} onSubmit={async (reason) => {
            if (!built.text) throw new Error(built.error);
            if (check && check.problems.length > 0) throw new Error("Fix the problems first: a saved plan must be valid.");
            const answer = editing
              ? await post<{ plan_id: number }>(`/v1/admin/plans/${encodeURIComponent(editing)}`, withPlan({ reason }, built.text))
              : await post<{ plan_id: number }>("/v1/admin/plans", withPlan({ firm: firm.trim(), template, reason }, built.text));
            navigate(`/plans/${answer.plan_id}`);
          }} />
        </aside>
      </div>
      <section>
        <h2>Preview</h2>
        {built.preview && <FlowGraph steps={[]} plan={built.preview} onSelect={() => {}} />}
      </section>
    </>
  );
}
