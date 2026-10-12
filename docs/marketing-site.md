# Public site

The public site at getkrith.com introduces Krith to accounting firms and finance teams and leads to one action: **Talk to us**. It is part of the firm app (`apps/frontend`), served by the edge Worker like every other page. The product's own pages use the Tray design system ([design system](design-system.md)); the public site has its own, described here.

## Pages and links

| Page | Route | What it holds |
| --- | --- | --- |
| Home | `/` | The hero (`#top`), Agents at work (`#how`) and "See one of your closes, prepared" (`#start`) |
| Contact | `/contact` | The contact form, three plain next steps and hello@getkrith.com |
| Security | `/security` | Nine short controls: authority, evidence, separation, encryption, uploads, AI, staff access, hosting and the firm's control |
| Privacy notice | `/privacy` | The notice: what is collected and why, who else sees it, the technology behind Krith and how long data is kept. Since 2026-10-11 it describes providers only by kind, with examples (Cloudflare and Amazon Web Services for hosting; OpenAI, Anthropic and Google for AI), says each firm's data is walled off from every other firm's, and offers a firm the current provider list on request. It names no internal setup: no database vendor or location beyond the United States, no sign-in or email vendor, no model routing |
| FAQ | `/faq` | "Asked and answered.": an index set like the home page's workpaper (each topic with its two-letter mark and count, the total over a double rule), then four topics (the product, working with the agents, your data, getting started) of questions that open one at a time (`<details>`), each topic ending in a link to the page that says more, and a ruled "Talk to us". Every answer says what Security, Privacy and About say, in the same terms and only what the service does today; change them together |
| About | `/about` | The page is about Krith first, in this order: "Krith means done" (from the Sanskrit *kṛta*), a short why, three principles that restate Security's authority, evidence and separation controls (change both together), **Working with Krith** (agents as a team rather than software to operate; three cards: hand it a goal, ask in plain words, start with the decisions), **Starting with the close** (why the close comes first and, stated as intent, what follows it; then "Talk to us"), and the company file at the bottom. It stays high level on purpose: how working with the agents feels, never feature lists, named integrations, figures or customers. Claims about the product hold for it today; what comes next is said as intent. The file lists who Krith is for, when and where it was founded (2026, New York) and its headquarters (New York City). It names no person: personal detail is kept out on purpose. The same facts and a one-line description are on the page as schema.org `Organization` data for search engines and anyone verifying the company |
| Log in | `/login` | **Continue with Google**, company accounts only, and "Talk to us" for firms on Microsoft 365 ([sign-in](sign-in.md)); a signed-in visitor goes to the workspace |
| Not found | any other path | A short page with a link home |

Every page shares one header and footer (`features/marketing/Layout.tsx`). Header: the wordmark (home), **How it works** (`/#how`), **Security**, **About**, **Log in** and **Talk to us** (`/contact`). Footer: How it works, Security, FAQ, About, Contact, Log in and Privacy notice. Every "Talk to us" goes to `/contact`; "How we protect client data" on the home page goes to `/security`. The client request page (`/request`) links to `/privacy` and `/security`. Nothing on the site links to another domain except the email address.

Earlier pages (`/subprocessors`, `/ai`, `/data-retention`, `/pilot-terms`) were removed on 2026-10-05: the providers list and retention summary moved into the privacy notice (the list itself was dropped on 2026-10-11 for providers by kind), and the AI commitments into Security. The detailed statements remain true of the service and belong in the workspace's Settings → Security and in a firm's written agreement. The site does not mention pilots or design partners.

**Accuracy rule.** Every statement on Security and Privacy follows what the service does today; when the code changes, change the page and the privacy notice's date. Names, firms and figures on the home page are illustrative and labelled so.

## Pages in the HTML itself

The app draws every page in the browser, so its built `index.html` alone is an empty shell. A visitor or checker that does not run JavaScript would see a blank page, so the build also writes the home, security, privacy, about and FAQ pages as static HTML: `npm run build -w @close/frontend` ends with `npm run prerender`, which renders `src/prerender.tsx` (the same routes as the app, `features/marketing/routes.tsx`) into `dist/static-home.html`, `security.html`, `privacy.html`, `about.html` and `faq.html`. Cloudflare serves `/security`, `/privacy`, `/about` and `/faq` from those files; the edge worker serves `/` from `static-home.html` and redirects `/static-home` to `/` (both in `run_worker_first`), because `index.html` stays the shell every app route loads. `/contact` stays the shell: its form sends only from the app. The build also writes the shell as `contact.html`, `login.html` and `request.html`, and the not-found page as `404.html`: the assets use `not_found_handling: "404-page"`, so any path that is neither a file nor one of these pages answers with a real 404 and that page (marked `noindex`). The workspace pages ask the assets for `/`, which is `index.html`, rather than for their own path.

Each page's title and description live in `features/marketing/meta.ts`. The build writes them, with the link-preview tags (`og:title`, `og:description`, `og:url`), into each page's static HTML, and the layout sets the title and description as a visitor moves between pages. `index.html` carries the home page's, which the shells and the client request page keep, plus the site-wide preview image `public/og.png` (1200×630: the home hero, captured with reduced motion and without the header's links and buttons; capture it again when the hero changes), `favicon.ico` (16, 32 and 48 px) and `apple-touch-icon.png`, both the wordmark's *k* over its double blue rule. `public/sitemap.xml` lists the public pages, `robots.txt` points to it, and `public/.well-known/security.txt` names the contact page and hello@getkrith.com for security reports; renew its `Expires` date before 2027-10-12. In the browser the app then renders the same page over the static copy, so it looks the same once loaded. A render must not read `window` (the build renders without a browser); effects may. `public/robots.txt` keeps crawlers off the workspace, admin, client request and sign-in pages.

Plain `http://` is redirected to `https://` by the zone's **Always Use HTTPS** setting in Cloudflare (SSL/TLS → Edge Certificates), not by the worker, which does not run for most static pages.

## Design

- **Two inks on white paper.** Black is what Krith prepares; blue (`--blue`, #1F3FBF) is what people decide: the decisions in the hero, "Signed by you", the reviewer's signature and every "Talk to us". Light only, by design.
- **Type.** Newsreader (serif), Geist (sans), Geist Mono and Mrs Saint Delafield (the signature), served from `features/marketing/fonts` with their SIL Open Font Licenses. The site loads no outside fonts or scripts, as the privacy notice says.
- **Isolation.** `apps/frontend/src/styles.css` holds only the public site. Every class carries a `k-` prefix (states use `is-`) and element rules sit under `.k-site` at no specificity, so the product shell's class names (`.pill`, `.row`, `.status`…) cannot reach these pages, and `scripts/layout-check.mjs` proves the shell takes nothing from this file.
- **Motion.** Everything moves slowly, pauses when off screen or in a hidden tab, has a visible pause control, and is still under `prefers-reduced-motion`.

## The hero: a city of ledger lines (`features/marketing/city.ts`)

A skyline is generated (seeded, so it is the same each time) and drawn as horizontal ledger lines whose thickness shades it. On load, about 18,000 short dashes, a month's transactions, drift and settle into the lines bottom-up; seven windows then light in blue as decisions waiting for the firm. Afterwards the plate keeps working: transactions arrive from the edges and land in buildings (the tally's transaction count rises), Krith works through one client every few seconds with a reading line and a label ("Preparing · client · job"), each pass ends as "Prepared" or as a new blue decision, and older decisions are taken by people ("Approved · M. Alvarez") and return to ink. Pointing at a blue window shows the decision. The live dot in the tally pauses it.

## Agents at work (`features/marketing/agents.ts`, `workpapers.ts`)

Four agents (Collector, Matcher, Investigator, Preparer), shown as collaborators' cursors, prepare a bank reconciliation: each stamps its initials beside the lines it did, the difference rolls to 0.00, the Preparer rules it off and drafts the entry, and a blue "You" cursor signs. The section then turns to the next of three workpapers and repeats. `workpapers.test.ts` proves each illustrative reconciliation ties. The agents' names describe work the product does (capturing sources, matching, bounded investigations and preparation); keep them in step with how the product names its agents.

## The contact form

1. The page posts `name`, `email`, `firm`, `note` and a hidden trap field `website` to `POST /v1/contact` (`services/backend/internal/transport/contact.go`).
2. The API accepts the form from `FRONTEND_ORIGIN` when the site and API are on different origins (the route is in its CORS preflight list). It needs no sign-in, and a visitor's ended workspace session does not stand in the way. It refuses a note without a name, a plain email address or a firm, or with control characters in a one-line field, or a note over 4,000 characters. A filled-in trap field is thanked and dropped. It allows five kept notes an hour from one address and sixty an hour in all. Both counts are taken from the stored notes, one note at a time under an advisory lock, so a burst cannot pass them together and a restarted API still holds them; only kept notes count. The address (Cloudflare's `CF-Connecting-IP`) is stored only as a one-way digest, cleared once its note is an hour old, as the privacy notice says. The page sends one `submission` key per note as written (an edit makes a new key): a retry after a lost answer returns the note already kept, with `forward: false`, so nothing is kept or forwarded twice, and a kept key sent with other words is refused (409). The note is kept in `contact_notes` (migration 123; platform rows only, under row-level security) and returned.
3. The edge Worker (`workers/edge/src/index.ts`, `contact.ts`) forwards each kept note through Cloudflare Email Routing's `send_email` binding `CONTACT_EMAIL`, from `website@<PUBLIC_APP_URL host>` to `CONTACT_FORWARD_ADDRESS`, with the visitor as Reply-To. Every header value is encoded and carries no line break. The visitor gets their answer at once; a failed forward is logged without its contents and the note stays kept.

### Where Krith's mail goes

Since 2026-10-05, getkrith.com's mail is hosted by **Google Workspace**: the apex MX records are Google's, and the team's mailbox is prashant@getkrith.com. **hello@getkrith.com** belongs in Google too, as an alias of that mailbox set in the Google Admin console: Cloudflare no longer sees apex mail. Contact notes go to the same mailbox once Cloudflare has verified it as a destination; until then `CONTACT_FORWARD_ADDRESS` stays on the verified personal inbox, so no note goes undelivered in between. The checklist below says what is done.

Cloudflare Email Routing stays enabled on the zone for two other jobs: client replies at `replies@in.getkrith.com`, a subdomain whose MX records are Cloudflare's ([email delivery](email-delivery.md)), and the Worker's contact-note sends. Because the apex MX is Google's, Cloudflare reports the apex as "misconfigured". That is expected: never accept Cloudflare's offer to fix or re-add the apex records, which would replace Google's MX and stop the team's mail. Keep these records at the apex:

| Record | Value | Why |
| --- | --- | --- |
| MX `getkrith.com` | Google's five `aspmx` hosts | Team mail |
| TXT `getkrith.com` | `v=spf1 include:_spf.mx.cloudflare.net include:_spf.google.com ~all` (one SPF record) | Google's mail, and the Worker's sends as website@getkrith.com |
| TXT `google._domainkey` | Google's DKIM key | Google's signature |
| TXT `cf2024-1._domainkey` | Cloudflare's DKIM key | The Worker's signature |
| TXT `_dmarc` | `v=DMARC1; p=none;` | Monitoring only, for now |

### Cloudflare and Google setup

- Email Routing enabled on getkrith.com (done 2026-10-05) and on `in.getkrith.com` (done 2026-10-04).
- **To do (owner):** add prashant@getkrith.com as a Cloudflare destination address and verify it from the link Cloudflare emails. The Worker can send only to a verified destination.
- **To do (owner):** delete the Cloudflare routing rule for hello@. It forwarded to a personal inbox and stopped receiving mail when the MX moved to Google.
- **To do (owner):** add hello@getkrith.com in Google Workspace as an alias of prashant@getkrith.com. Until then, mail to hello@ bounces.
- **Then:** set the production GitHub variable `CONTACT_FORWARD_ADDRESS` to prashant@getkrith.com and run Deploy production; a variable reaches the Worker only with a deploy. It points to the verified personal inbox meanwhile. Without it, notes are kept but not forwarded. Then send one contact note and confirm it arrives.

The inbox is a convenience; the record is the staff console's **Contact notes** page (`platform:read`), which lists every kept note newest first. A note whose email failed, or that arrived while `CONTACT_FORWARD_ADDRESS` was unset, is read and answered there.

## Checks

`npm run check:layout` opens every public page directly and from the home page's links, at desktop and phone width, alongside the product's pages. `npm test` runs the workpaper and contact-email tests; `go test ./internal/transport -run Contact` runs the form's validation, limits and storage against local PostgreSQL.

Since 2026-10-11 neither Security nor Privacy describes the internal setup (database vendor and location beyond the United States, sign-in and email vendors, model routing, the context machine): they name providers only by kind, with examples, and offer a firm the current provider list on request. Security still says that a scanned page a model reads as an image is sent whole, without text redaction. The routing detail lives in the workspace's settings and in [the LLM gateway](llm-gateway.md). Redaction covers document text and assistant questions (`asked()` in `written_answers.go`); whole PDF page images, file names and Ask Work commands reach a model as they are, and Security says so. OpenAI images require a separate operator opt-in. A personal subscription's data-handling and model-improvement settings must be verified by the operator before activation (nothing checks them at startup, and there is no owner allowlist: [context machine](context-machine-deployment.md)); Security's no-training sentence rests on that activation rule. These disclosures do not claim that a staged model pin is supported by the account.
