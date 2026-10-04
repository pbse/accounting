// Temporary main-only cutover job; never log or export credential values.
import { credentials, install, secretNames, validateCredential } from './cloudflare-credentials.mjs';
import { writeFileSync } from 'node:fs';
if (process.argv.length !== 3 || !['--production','--staging'].includes(process.argv[2])) throw new Error('Select exactly --production or --staging');
const production = process.argv[2] === '--production';
let db;
try { db = new URL(process.env.DATABASE_URL); } catch { throw new Error('Missing legacy runtime database credential'); }
const role = decodeURIComponent(db.username);
if (!/^[a-zA-Z_][a-zA-Z0-9_]{0,62}$/.test(role)) throw new Error('Runtime database role needs operator verification');
const transfers = Object.entries(credentials).map(([app,names]) => [app,Object.fromEntries(names.filter(name => process.env[name]).map(name => [name,validateCredential(name,process.env[name])]))]);
// Fail before mutation for invalid values or missing/inaccessible destinations.
for (const [app] of transfers) secretNames(app,production);
for (const [app,values] of transfers) install(app,values,production);
writeFileSync('runtime-role.txt',role+'\n');
