import { Background, Handle, Position, ReactFlow, type Edge, type Node, type NodeProps } from "@xyflow/react";
import "@xyflow/react/dist/style.css";

export type Step = {
  key: string;
  seq: number;
  kind: string;
  state: string;
  attempts: number;
  retry_at: string | null;
  wake_at: string | null;
  waiting_on: string;
  error: string;
  actor: string;
  note: string;
  started_at: string;
  finished_at: string | null;
  child_run_id: number | null;
  activity?: string;
  input: unknown;
  output: unknown;
};

export type PlanNode = {
  id: string;
  after?: string[];
  when?: string;
  activity?: string;
  workflow?: string;
  wait?: { event?: string; duration?: string; timeout?: string };
  approval?: { permission: string; subject?: string };
};

type NodeData = { label: string; kind: string; state: string; detail?: string; across: boolean };

// A node of the flow: its name, what kind of step it is, its state, and what it waits for.
function StepNode({ data, selected }: NodeProps<Node<NodeData>>) {
  return (
    <div className={`flow-node state-${data.state}${selected ? " selected" : ""}`}>
      <Handle type="target" position={data.across ? Position.Left : Position.Top} />
      <strong>{data.label}</strong>
      <span className="flow-kind">{data.kind}</span>
      <span className="flow-state">{data.state.replaceAll("_", " ")}</span>
      {data.detail && <small>{data.detail}</small>}
      <Handle type="source" position={data.across ? Position.Right : Position.Bottom} />
    </div>
  );
}

const nodeTypes = { step: StepNode };

function describe(step?: Step): string | undefined {
  if (!step) return undefined;
  if (step.state === "waiting" && step.waiting_on) return `waiting for ${step.waiting_on}`;
  if (step.state === "waiting" && step.retry_at) return `retrying ${new Date(step.retry_at).toLocaleString()}`;
  if (step.state === "waiting" && step.wake_at) return `until ${new Date(step.wake_at).toLocaleString()}`;
  if (step.state === "unknown") return "outcome unknown: check the provider";
  if (step.attempts > 1) return `${step.attempts} attempts`;
  return undefined;
}

// A code workflow's steps in the order reached, top to bottom. Saved values (Now, Random) are left
// out of the drawing; the step list below the graph shows everything.
function stepGraph(steps: Step[]): { nodes: Node<NodeData>[]; edges: Edge[] } {
  const shown = steps.filter((s) => s.kind !== "value");
  const nodes = shown.map((s, i) => ({
    id: s.key,
    type: "step",
    position: { x: 0, y: i * 120 },
    data: { label: s.activity ? `${s.key} · ${s.activity}` : s.key, kind: s.kind, state: s.state, detail: describe(s), across: false },
  }));
  const edges = shown.slice(1).map((s, i) => ({ id: `${shown[i].key}->${s.key}`, source: shown[i].key, target: s.key }));
  return { nodes, edges };
}

function planKind(n: PlanNode): { kind: string; detail: string } {
  if (n.activity) return { kind: "activity", detail: n.activity };
  if (n.workflow) return { kind: "child", detail: n.workflow };
  if (n.approval) return { kind: "approval", detail: `needs ${n.approval.permission}` };
  if (n.wait?.event) return { kind: "event", detail: n.wait.event };
  return { kind: "sleep", detail: n.wait?.duration ?? "" };
}

// A plan's nodes, left to right by how deep they sit behind their `after` nodes; nodes not reached
// yet are drawn dimmed.
function planGraph(plan: PlanNode[], steps: Step[]): { nodes: Node<NodeData>[]; edges: Edge[] } {
  const byKey = new Map(steps.map((s) => [s.key, s]));
  const depth = new Map<string, number>();
  const rows = new Map<number, number>();
  const nodes = plan.map((n) => {
    const d = Math.max(-1, ...(n.after ?? []).map((a) => depth.get(a) ?? 0)) + 1;
    depth.set(n.id, d);
    const row = rows.get(d) ?? 0;
    rows.set(d, row + 1);
    const step = byKey.get(`node:${n.id}`);
    const { kind, detail } = planKind(n);
    const state = step ? (step.output === "skipped" ? "skipped" : step.state) : "not_reached";
    return {
      id: `node:${n.id}`,
      type: "step",
      position: { x: d * 280, y: row * 130 },
      data: { label: n.id, kind, state, detail: describe(step) ?? detail, across: true },
    };
  });
  const edges = plan.flatMap((n) =>
    (n.after ?? []).map((a) => ({ id: `${a}->${n.id}`, source: `node:${a}`, target: `node:${n.id}`, label: n.when && n.when.startsWith(`${a}.`) ? n.when.slice(a.length + 1) : undefined })),
  );
  return { nodes, edges };
}

export function FlowGraph({ steps, plan, selected, onSelect }: { steps: Step[]; plan?: PlanNode[]; selected?: string; onSelect: (key: string) => void }) {
  const graph = plan ? planGraph(plan, steps) : stepGraph(steps);
  const nodes = graph.nodes.map((n) => ({ ...n, selected: n.id === selected }));
  const { edges } = graph;
  if (nodes.length === 0) return <p>No step has run yet.</p>;
  return (
    <div className="flow" aria-hidden="true">
      <ReactFlow
        // A new layout is fitted into view again, so an added or moved step never falls outside it.
        key={nodes.map((n) => `${n.id}@${n.position.x},${n.position.y}`).join(" ")}
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodeClick={(_, node) => onSelect(node.id)}
        nodesDraggable={false}
        nodesConnectable={false}
        fitView
      >
        <Background />
      </ReactFlow>
    </div>
  );
}
