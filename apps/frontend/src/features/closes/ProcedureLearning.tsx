import { useState, type FormEvent } from "react";
import { workspaceRequest } from "./workspace";
import type { EvidenceFile } from "./Evidence";

type Draft = { id:string; client_entity:string; kind:string; method:string; instructions:string; expense_account:string; prepaid_account:string; source_id:string; source_close_id:string; source_quote:string; source_draft_ids:string[]; source_edits:{id:string;close_id:string}[]; activated_version_id:string; example_count:number; revision:number; engine:string; model:string };
type Example = { id:string; close_id:string; entity:string; period:string; digest:string; review_id:string; approved_by:string; reason:string };
type Impact = { previous_id:string; impact_digest:string; total_closes:number; has_more:boolean; closes:{close_id:string;entity:string;period:string;draft_count:number;pinned_source_id:string}[] };
const methods:Record<string,{value:string;label:string}[]> = {
  prepaid:[{value:"equal_full_months_v1",label:"Equal full months"},{value:"actual_days_v1",label:"Inclusive actual days"},{value:"equal_full_months_v1+mid_month",label:"Depreciation: equal months, mid-month convention"},{value:"declining_balance_v1",label:"Depreciation: declining balance"},{value:"declining_balance_v1+mid_month",label:"Depreciation: declining balance, mid-month convention"}],
  accrual:[{value:"fixed_incurred_amount_v1",label:"Fixed incurred amount"},{value:"actual_invoice_true_up_v1",label:"Actual invoice true-up"},{value:"fixed_recurring_incurred_v1",label:"Fixed recurring incurred"},{value:"pro_rata_service_days_v1",label:"Pro-rata share of a service contract"}],
};

export function ProcedureLearning({closeId,entity,onActivated}:{closeId:string;entity:string;onActivated:()=>void}) {
  const [kind,setKind]=useState("prepaid");
  const [scope,setScope]=useState("client");
  const [method,setMethod]=useState(methods.prepaid[0].value);
  const [source,setSource]=useState("");
  const [files,setFiles]=useState<EvidenceFile[]>([]);
  const [drafts,setDrafts]=useState<Draft[]>([]);
  const [selected,setSelected]=useState<Draft>();
  const [examples,setExamples]=useState<Example[]>([]);
  const [impact,setImpact]=useState<Impact>();
  const [busy,setBusy]=useState(false);
  const [error,setError]=useState("");
  const [message,setMessage]=useState("");
  const [sourceText,setSourceText]=useState("");
  const base="/"+closeId+"/procedures/drafts";
  async function load(k=kind) {
    try {
      const [evidence,list]=await Promise.all([
        workspaceRequest("/"+closeId+"/evidence") as Promise<EvidenceFile[]>,
        workspaceRequest(base+"?kind="+k) as Promise<{drafts:Draft[]}>,
      ]);
      setFiles(evidence.filter(f=>f.purpose==="procedure"&&f.extraction_state==="ready"&&!f.superseded_by&&!f.procedure_id));
      setDrafts(list.drafts);
      setSelected(current=>list.drafts.find(d=>d.id===current?.id));
    } catch(e) {setError(e instanceof Error?e.message:"Procedure learning unavailable.");}
  }
  async function choose(d:Draft) {
    setSelected(d);setImpact(undefined);setExamples([]);setSourceText("");setError("");
    try {const result=await workspaceRequest(base+"/"+d.id+"/examples") as {examples:Example[]};setExamples(result.examples);}
    catch(e){setError(e instanceof Error?e.message:"Examples unavailable.");}
  }
  async function create(fromEdits:boolean) {
    setBusy(true);setError("");setMessage("");
    try {
      const d=await workspaceRequest(base,{client_entity:scope==="client"?entity:"",kind,method,source_id:fromEdits?"":source,from_edits:fromEdits}) as Draft;
      await load();await choose(d);
      setMessage(`${d.engine==="openai"?"AI draft":"Local review prompt"} saved as an inactive proposal. Inspect its source and rewrite the instructions before approving an example.`);
    } catch(e){setError(e instanceof Error?e.message:"Could not draft the rule.");}
    finally{setBusy(false);}
  }
  async function upload(e:FormEvent<HTMLFormElement>) {
    e.preventDefault();const form=e.currentTarget;
    const data=new FormData(form);data.set("purpose","procedure");
    setBusy(true);setError("");
    try {
      const saved=await workspaceRequest("/"+closeId+"/evidence",data) as {id:string};
      form.reset();await load();setSource(saved.id);
      setMessage("Policy source saved without starting preparation. PDF text appears after extraction.");
    } catch(err){setError(err instanceof Error?err.message:"Policy upload failed.");}
    finally{setBusy(false);}
  }
  async function revise(e:FormEvent<HTMLFormElement>) {
    e.preventDefault();if(!selected)return;
    const data=new FormData(e.currentTarget);setBusy(true);setError("");
    try {
      await workspaceRequest(base+"/"+selected.id+"/revise",{revision:selected.revision,instructions:data.get("instructions"),expense_account:data.get("expense_account")??"",prepaid_account:data.get("prepaid_account")??""});
      const result=await workspaceRequest(base+"?kind="+kind) as {drafts:Draft[]};
      setDrafts(result.drafts);setSelected(result.drafts.find(d=>d.id===selected.id));
      setMessage("Draft revised. The rule remains inactive.");
    } catch(err){setError(err instanceof Error?err.message:"Draft revision failed.");}
    finally{setBusy(false);}
  }
  async function approve(e:FormEvent<HTMLFormElement>,example:Example) {
    e.preventDefault();if(!selected)return;
    const data=new FormData(e.currentTarget);setBusy(true);setError("");
    try {
      await workspaceRequest(base+"/"+selected.id+"/examples",{workpaper_id:example.id,digest:example.digest,review_id:example.review_id,draft_revision:selected.revision,reason:data.get("reason"),confirmed:data.get("confirmed")==="on"});
      const list=await workspaceRequest(base+"?kind="+kind) as {drafts:Draft[]};
      const updated=list.drafts.find(d=>d.id===selected.id);
      setDrafts(list.drafts);if(updated)await choose(updated);
      setMessage("Example approved after exact calculator replay.");
    } catch(err){setError(err instanceof Error?err.message:"Example approval failed.");}
    finally{setBusy(false);}
  }
  async function preview() {
    if(!selected)return;setBusy(true);setError("");setImpact(undefined);
    try {setImpact(await workspaceRequest("/procedures?impact_kind="+selected.kind+"&client_entity="+encodeURIComponent(selected.client_entity)) as Impact);}
    catch(e){setError(e instanceof Error?e.message:"Affected clients unavailable.");}
    finally{setBusy(false);}
  }
  async function inspectSource() {
    if(!selected?.source_id)return;
    setError("");
    try {
      const blob=await workspaceRequest("/"+selected.source_close_id+"/evidence/"+selected.source_id+"/text",undefined,true) as Blob;
      setSourceText(await blob.text());
    } catch(e){setError(e instanceof Error?e.message:"Policy source unavailable.");}
  }
  async function activate(e:FormEvent<HTMLFormElement>) {
    e.preventDefault();if(!selected||!impact)return;
    const data=new FormData(e.currentTarget);setBusy(true);setError("");
    try {
      await workspaceRequest(base+"/"+selected.id+"/activate",{previous_id:impact.previous_id,impact_digest:impact.impact_digest,draft_revision:selected.revision,reason:data.get("reason"),confirmed:data.get("confirmed")==="on"});
      await load();onActivated();setImpact(undefined);
      setMessage("Rule activated for new context. Existing pinned closes need reviewed application.");
    } catch(err){setError(err instanceof Error?err.message:"Rule activation failed.");}
    finally{setBusy(false);}
  }
  return <details onToggle={e=>{if(e.currentTarget.open)void load();}}>
    <summary>Learn a procedure from evidence or reviewer edits</summary>
    <p>A proposal is never policy. Choose a supported method, inspect and edit the draft, approve a current workpaper example, then preview affected clients.</p>
    <p>When OpenAI is configured, drafting sends the selected policy text (up to 16 KiB) or up to 30 approved field edits to OpenAI. Without it, drafting creates a local review prompt. No draft activates itself.</p>
    {error&&<p role="alert">{error}</p>}{message&&<p role="status">{message}</p>}
    <fieldset disabled={busy}><legend>Start an unapproved rule</legend>
      <form onSubmit={e=>void upload(e)}><label>Upload firm checklist or policy <input name="file" type="file" accept=".txt,.csv,.pdf" required/></label><button>Save policy source</button></form>
      <button type="button" onClick={()=>void load()}>Refresh policy sources</button>
      <label>Work kind <select value={kind} onChange={e=>{const k=e.target.value;setKind(k);setMethod(methods[k][0].value);setSelected(undefined);void load(k);}}><option value="prepaid">Prepaid</option><option value="accrual">Accrual</option></select></label>
      <label>Scope <select value={scope} onChange={e=>setScope(e.target.value)}><option value="client">This client</option><option value="firm">Firm default</option></select></label>
      <label>Method <select value={method} onChange={e=>setMethod(e.target.value)}>{methods[kind].map(m=><option key={m.value} value={m.value}>{m.label}</option>)}</select></label>
      <label>Policy source <select value={source} onChange={e=>setSource(e.target.value)}><option value="">Choose ready source from this close</option>{files.map(f=><option key={f.id} value={f.id}>#{f.id} · {f.name}</option>)}</select></label>
      <button type="button" disabled={!source} onClick={()=>void create(false)}>Draft from document</button>
      <button type="button" onClick={()=>void create(true)}>Draft from prior reviewer edits</button>
    </fieldset>
    <h4>Proposals</h4>
    {drafts.length===0&&<p>No proposals for this work kind yet.</p>}
    <ul>{drafts.map(d=><li key={d.id}><button type="button" onClick={()=>void choose(d)}>#{d.id} · {d.client_entity||"Firm default"} · {d.method} · {d.activated_version_id?"activated v"+d.activated_version_id:"inactive"}</button></li>)}</ul>
    {selected&&<section>
      <h4>Proposal #{selected.id}</h4>
      <p>{selected.engine==="openai"?`AI draft · ${selected.model}`:"Local review prompt"} · {selected.activated_version_id?"activated after accountant approval":"inactive until accountant approval"}</p>
      <p>Source: {selected.source_id?<><a href={"/dashboard/closes/"+selected.source_close_id}>Document #{selected.source_id} in its close</a> <button type="button" onClick={()=>void inspectSource()}>Inspect extracted source</button></>:<>Reviewer drafts {selected.source_edits.map(edit=><a key={edit.id} href={"/dashboard/closes/"+edit.close_id}>#{edit.id} </a>)}</>}. {selected.source_quote&&<>Cited passage: “{selected.source_quote}”</>}</p>
      {sourceText&&<pre style={{whiteSpace:"pre-wrap",overflowWrap:"anywhere",maxHeight:"20rem",overflow:"auto"}}>{sourceText}</pre>}
      <p>Method boundary: {selected.method}. {selected.example_count} approved example(s).</p>
      <form key={selected.revision} onSubmit={e=>void revise(e)}><fieldset disabled={busy||!!selected.activated_version_id||selected.example_count>0}><legend>Accountant edits before example approval</legend>
        <label>Reviewed instructions <textarea name="instructions" required maxLength={4000} defaultValue={selected.instructions}/></label>
        {selected.kind==="prepaid"&&<><label>Expense account hint <input name="expense_account" maxLength={200} defaultValue={selected.expense_account}/></label><label>Prepaid account hint <input name="prepaid_account" maxLength={200} defaultValue={selected.prepaid_account}/></label></>}
        <button>Save revised draft</button>
      </fieldset></form>
      <h4>Approved workpapers for regression</h4>
      {examples.length===0&&<p>No approved workpaper uses this method in scope. Prepare and approve a synthetic example first, then reopen this proposal.</p>}
      <ul>{examples.map(ex=><li key={ex.id}>
        <a href={"/dashboard/closes/"+ex.close_id}>{ex.entity} · {ex.period} · workpaper #{ex.id}</a>
        {ex.approved_by?<p>Regression approved by {ex.approved_by}: {ex.reason}</p>:!selected.activated_version_id&&<form onSubmit={e=>void approve(e,ex)}>
          <label>Accountant approval reference and expected behavior <input name="reason" required maxLength={1000}/></label>
          <label><input name="confirmed" type="checkbox" required/>I reviewed this example and authorize its exact result as a regression baseline.</label>
          <button disabled={busy}>Approve example</button>
        </form>}
      </li>)}</ul>
      {!selected.activated_version_id&&<><button type="button" disabled={busy||selected.example_count===0} onClick={()=>void preview()}>Preview affected clients</button>
        {impact&&<form onSubmit={e=>void activate(e)}>
          <p>{impact.total_closes} close(s) in scope. Publishing leaves pinned evidence unchanged. {impact.has_more&&"Only the latest 100 are shown."}</p>
          <ul>{impact.closes.map(c=><li key={c.close_id}><a href={"/dashboard/closes/"+c.close_id}>{c.entity} · {c.period}</a> · {c.draft_count} drafts · {c.pinned_source_id?"pinned source #"+c.pinned_source_id:"no pinned procedure"}</li>)}</ul>
          <label>Accountant approval and rule rationale <textarea name="reason" required maxLength={2000}/></label>
          <label><input name="confirmed" type="checkbox" required/>I reviewed the rule, source, regression example and affected clients.</label>
          <button disabled={busy}>Activate reviewed rule</button>
        </form>}
      </>}
    </section>}
  </details>;
}
