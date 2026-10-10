import { execFileSync } from 'node:child_process';
import { setTimeout } from 'node:timers/promises';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

export async function check({ repository, sha, query, verify, pause = setTimeout, attempts = 13 }) {
  for (let attempt = 0; attempt < attempts; attempt++) {
    // Association can lag the merge webhook; retry metadata without rerunning CI.
    const prs = query(['api', `repos/${repository}/commits/${sha}/pulls`]);
    const heads = [...new Set(prs.filter(p => p.merged_at && p.merge_commit_sha === sha && p.base.ref === 'main').map(p => p.head.sha))];
    let pending = heads.length === 0;
    for (const head of heads) {
      const runs = query(['run', 'list', '--repo', repository, '--workflow', 'ci.yml', '--commit', head, '--event', 'pull_request', '--limit', '1', '--json', 'databaseId,status,conclusion']);
      if (!runs.length || runs.some(r => r.status !== 'completed')) pending = true;
      for (const run of runs.filter(r => r.status === 'completed' && r.conclusion === 'success')) {
        if (await verify(run.databaseId, head)) return;
        pending = true; // Artifact metadata can also lag run completion.
      }
    }
    if (!pending) throw new Error('The merged pull request did not pass CI');
    if (attempt + 1 < attempts) await pause(15000);
  }
  throw new Error('Merged PR/CI evidence is still unavailable after three minutes; deployment was not started. For incomplete CI or uploads, rerun the merged PR CI and then retry this deployment. An older workflow without provenance or a direct main push requires a manually validated Deploy production dispatch.');
}

export function hasProvenance(artifacts) {
 if (artifacts.some(a=>a.name==='ci-provenance' && !a.expired)) return true;
 if (artifacts.some(a=>a.name==='ci-provenance' && a.expired)) throw new Error('CI provenance expired; rerun CI before automatic deployment.');
 return false;
}

if (process.argv[1]?.endsWith('/check-merged-ci.mjs')) {
  const gh = args => execFileSync('gh', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'inherit'] });
  const repository = process.env.GITHUB_REPOSITORY;
  const tree = execFileSync('git', ['rev-parse', `${process.env.GITHUB_SHA}^{tree}`], { encoding: 'utf8' }).trim();
  await check({ repository, sha: process.env.GITHUB_SHA, query: args => JSON.parse(gh(args)), verify: async (id, head) => {
    const artifacts = JSON.parse(gh(['api', `repos/${repository}/actions/runs/${id}/artifacts`])).artifacts;
    if (!hasProvenance(artifacts)) return false;
    const dir = mkdtempSync(join(tmpdir(), 'krith-ci-proof-'));
    try {
      gh(['run', 'download', String(id), '--repo', repository, '--name', 'ci-provenance', '--dir', dir]);
      const proof = JSON.parse(readFileSync(join(dir, 'provenance.json'), 'utf8'));
      if (proof.head !== head || proof.tree !== tree) throw new Error('The merged file tree differs from the tree CI tested. Validate current main and dispatch Deploy production manually, or merge a new PR based on current main with passing exact-tree CI.');
      return true;
    } finally { rmSync(dir, { recursive: true, force: true }); }
  }});
  console.log('The exact merged file tree passed CI; reusing that run.');
}
