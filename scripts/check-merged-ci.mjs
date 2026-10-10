import { execFileSync } from 'node:child_process';
import { setTimeout } from 'node:timers/promises';

export function ciDecision(runs) {
  if (runs.some(r => r.status === 'completed' && r.conclusion === 'success')) return 'passed';
  return runs.some(r => r.status !== 'completed') ? 'pending' : 'failed';
}

export async function check({ repository, sha, query, pause = setTimeout, attempts = 13 }) {
  // Use only merged PRs associated with this exact main commit, not any passing ancestor.
  const prs = query(['api', `repos/${repository}/commits/${sha}/pulls`]);
  const heads = [...new Set(prs.filter(p => p.merged_at && p.merge_commit_sha === sha && p.base.ref === 'main').map(p => p.head.sha))];
  if (!heads.length) throw new Error('The main commit has no merged pull request with CI evidence');
  for (let attempt = 0; attempt < attempts; attempt++) {
    const decisions = heads.map(head => ciDecision(query(['run', 'list', '--repo', repository, '--workflow', 'ci.yml', '--commit', head, '--event', 'pull_request', '--limit', '1', '--json', 'status,conclusion'])));
    if (decisions.includes('passed')) return;
    if (!decisions.includes('pending')) throw new Error('The merged pull request did not pass CI');
    if (attempt + 1 < attempts) await pause(15000);
  }
  throw new Error('CI is still running after three minutes; deployment was not started. Rerun after CI passes.');
}

if (process.argv[1]?.endsWith('/check-merged-ci.mjs')) {
  await check({ repository: process.env.GITHUB_REPOSITORY, sha: process.env.GITHUB_SHA,
    query: args => JSON.parse(execFileSync('gh', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'inherit'] })) });
  console.log('The merged pull request passed CI; reusing that run.');
}
