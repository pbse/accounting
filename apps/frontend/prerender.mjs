// Fills each public page's static HTML from the built page (dist/index.html) and the server render (src/prerender.tsx).
// dist/index.html itself stays the empty shell every other route loads.
import { readFileSync, rmSync, writeFileSync } from "node:fs";

const { pages, shells, pageMeta, render } = await import("./.prerender/prerender.js");
const shell = readFileSync("dist/index.html", "utf8");

// A page's own title, description, link preview and canonical address, in place of the home page's that index.html
// carries. The page that is not found is kept out of search results.
const escape = (text) => text.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");
function head(html, path) {
  const { title, description } = pageMeta(path);
  const set = (attribute, value) => {
    const pattern = new RegExp(`(<meta ${attribute} content=")[^"]*(")`);
    if (!pattern.test(html)) throw new Error(`dist/index.html has no <meta ${attribute}>`);
    html = html.replace(pattern, `$1${escape(value)}$2`);
  };
  html = html.replace(/<title>[^<]*<\/title>/, `<title>${escape(title)}</title>`);
  set('name="description"', description);
  set('property="og:title"', title);
  set('property="og:description"', description);
  set('property="og:url"', `https://getkrith.com${path}`);
  if (path === "/404") return html.replace("</title>", '</title><meta name="robots" content="noindex" />');
  return html.replace("</title>", `</title><link rel="canonical" href="https://getkrith.com${path}" />`);
}

for (const [path, file] of Object.entries(pages)) {
  const html = shell.replace('<div id="root"></div>', `<div id="root">${render(path)}</div>`);
  if (html === shell) throw new Error("dist/index.html has no empty root to fill");
  writeFileSync(`dist/${file}`, head(html, path));
}
// The client request page is a firm's, not the public site's: it keeps index.html's head.
for (const [path, file] of Object.entries(shells)) writeFileSync(`dist/${file}`, path === "/request" ? shell : head(shell, path));
rmSync(".prerender", { recursive: true });
