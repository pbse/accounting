# Assistant

> This describes the assistant as built. Its rebuild into an agent that chooses its own reads and proposes actions as cards is designed in [agent-chat.md](agent-chat.md); as each part of that plan ships, the behavior here is replaced.

The **Agent** button (bottom right of every signed-in workspace page, and in the top bar and rail) opens a panel that answers questions from the saved work you can already see (migration 072). It shows only in a signed-in or unlocked workspace, and it remembers whether it was open in this browser; nothing else is kept in the browser.

## Context and history

A member's conversations are chats (sessions, migration 138), each in one scope and titled by its first question. A question continues the scope's latest chat while it is less than 12 hours quiet and otherwise starts a new one; "+" starts one at will; the rail's chats menu lists the five latest with "All chats", and the panel's list shows every chat grouped by day with its scope and a filter; a chat is renamed or deleted from the panel's menu. `GET …/chat?session=` reads one chat (without it, the current one or none), `POST …/chat` takes `session_id` (an id, `new`, or empty) and `picked` (a chat opened on purpose is continued whatever its age; the current one rolls over after 12 quiet hours), `GET /v1/closes/chat/sessions` lists them (pages of 50, `offset`, `has_more`) and `POST /v1/closes/chat/sessions/{id}` renames (`title`) or deletes. The model sees one chat's turns only. Clearing deletes the chat.


- On a client close the conversation is about that close; its chip shows the client and month. On a draft, a second chip names the draft, and questions that name the draft or workpaper are about it; other questions stay about the close.
- Anywhere else the conversation is firm-wide.
- Each conversation belongs to the member who wrote it: nobody else reads it, including firm administrators. Switching to another close opens that close's conversation from empty; a question or answer never carries over to another client, and a late answer for the previous page is dropped.
- A close conversation is reachable only while you can access the close: firm membership and the client assignment are checked on every read and message. A draft must belong to the close.
- A conversation keeps and shows its latest 200 messages. **Clear** deletes it. Deleting the close deletes its conversations, and removing a member from the firm deletes theirs (a message being saved at that moment waits for the removal and is refused). Firm-wide messages older than 30 days are never shown, and the worker deletes them hourly.

## What it answers

With **Agent chat** on (Settings → Policies: for client closes, or firm-wide; migration 133), a free-text message goes to the agent: the model chooses what to read from the registry of tools below, each read runs through the close routes under your own permissions, and the answer is written in a separate step that must bind every figure and sentence to a read that was sent. The panel shows each step as it runs (what was read, refused, unavailable or proposed), then the answer with chips linking the fields it was bound to; the panel's look (composer, bubbles, steps, cards, phone sheet, motion) is set out in the [design system](design-system.md#the-agent-comes-first). An action the agent proposes is a card you confirm; nothing runs before that. A round's reads run together under the loop's deadline. The first round reserves its own model call and the answer's, so a round never spends the last call the answer needs. Each attempt counts its model calls against the firm's daily limit and records only its outcome, rounds, tool calls and a trace without content (migration 135). Chat execution is bounded to one minute, with the last 25 seconds reserved for its supported answer; the six-round/eight-read limits remain. A singular firm-wide question about “this close” or “this month” asks for a client/month when the conversation has not established one. Timeout, sign-in, busy/usage-limit and incomplete-response errors have distinct messages and diagnostic codes; no provider response body or prompt is logged.

With the agent off, or outside a close while the firm allows it only for closes, a message is answered as before: matched to one of the fixed reads below by its words, without a model, and anything else gets a short list of what the panel can answer. A [command](#commands) runs its fixed read or card at every level.

| Scope | The agent may read | Through |
| --- | --- | --- |
| A close | where it stands; what blocks sign-off; the tie-out; changes from last month; exceptions; the evidence list and a document's text; a draft and its changes; the management pack, open items, cash plan and budget comparison; Xero's reconciled flags; the proposed-state preview; the close's jobs; request drafts and what a request would ask; the preparation preview; the agent's plan; the approved procedures; a search within the close | the close's own routes, with the close pinned whatever the model sends |
| Firm-wide (only at the firm level) | the closes you can open; Work's lists; the review queue; the morning brief; the track record; the closes handed to the agent; a search across your closes; which closes a portfolio goal selects; and, for a close it names by id, every close read above | the same routes; a close named by the model is one `list_closes` returned and the route checks your access |
| Inside a close at the firm level | the two cross-client reads too (`search`, `list_closes`), so "compare with my other clients" is possible | |

The agent may propose, never run: preparing a close, rechecking failed proposals, stopping unresolved work, drafting a client request, asking a person to review a draft, handing a close to the agent, and running a portfolio goal on the closes its preview listed. Each becomes a [card](#action-cards). One proposal per message: a second is refused and the agent says it follows once the first is confirmed. Opening a new close from chat is not offered yet.

Every answer is checked before it is kept: each support entry names a step that ran and a JSON pointer whose value it cites (or, for a document's text, a verbatim quote of at least 8 characters); every amount, count, percentage or date in the text must be such a value, or a figure inside a cited prose field; every sentence of a factual answer needs support that bears on it (a figure of the sentence, the value's own words or the field's name appear in the sentence), and a state it asserts ("is reconciled", "ties") must itself be a cited value of that sentence; a help, refusal or card answer never says an action ran; a help, refusal or card answer may carry no figure, no client, account or document name, no state of a close and no link a read did not supply, and each of its sentences speaks for the agent or to you ("I can…", "Ask…", "Here is…") and never observes the books (no "I can see a cash surplus"); a proposal's arguments are checked before its card is shown (a hand-over needs its finish line, a stop its reason, a review request its draft and note); a figure is bound by a support entry of its own sentence; a money figure must be stated as what its field measures: the metric or line named next to it is the field's (receivables never support "net income", gross profit never supports net profit), a balance is not stated as a change nor a change as a balance, the direction stated ("rose", "fell") is the change's sign, the prior month's amount is said to be prior, a forecast or scenario figure is said to be one, a budget is said to be the budget and year to date is not a month, and the client, month, currency and basis the sentence names are the field's; book cash is never called available, a stale read or a superseded or corrected publication is said to be stale; a cause is stated only when a read gives it or as a possibility, and drivers that do not add up to a change must say what remains; a total, difference or percentage the agent computed is cited as derived from the fields it names and recomputed exactly on the server; citations name each field by its line and kind (Net income · change), not its path; a portfolio goal must be proposed in the words it was previewed with. A failing answer is not saved: the reply says none could be supported and links what was read. The text goes out only once the pair is saved; before saving, your membership and your access to every close the reads touched are checked, and after a read without lineage the clients you can open must be the ones the reads were made with; a change means 403 and nothing saved.

The fixed reads, used by commands and by free text when the agent is off:

| Where | Ask about | From |
| --- | --- | --- |
| A close | where it stands | the close brief |
| | what blocks sign-off | sign-off readiness |
| | balances that don't tie, or a named account | the balance sheet tie-out |
| | what changed from last month, or a named account | changes from last month |
| | exceptions in the ledger capture | the Exceptions panel |
| | whether the ledger capture is certified | the close brief's ledger line |
| A close | find … (also search, look for, where is, show me) | search, in this close |
| | prepare, recheck or stop its work | an action card ([below](#action-cards)) |
| | get it ready for review, or signed off (by a day; "every month", "each month" or "monthly" too) | a card handing the close to the agent ([close goals](close-coordinator.md#hand-the-close-to-the-agent)), with the monthly hand-over ticked when asked for |
| | the plan | the agent's plan for the close and where each step stands, or the plan it would follow if handed the close |
| A draft | this draft | its review state, blockers and questions |
| Firm-wide | find … | search, across your closes |
| | what needs you today | the morning brief |
| | the review queue | drafts ready for review |
| | blocked work | Work's blocked view (the count is Work's whole total; five are listed) |
| | show or list the closes a goal selects ("show my clients due Friday", "show closes waiting on statements") | Work's own selection for you: how many match ("more than 100" when capped), the first ten with their due dates, each linked |
| | delegated closes, or what the agent is working on, asked plainly ("how are my delegated closes doing?") | **Handed to the agent**: how many, and the first ten in Work's words, due dates and who handed them over |

A follow-up to a fixed read that refers back ("that", "it", "same") and names no account keeps the account of your previous question in the same close conversation, when that question was the same kind; otherwise nothing carries over.

### Conversation and lineage

The agent sees the last 20 turns of the conversation as plain text (your messages and the saved replies redacted as they are sent, never a read's content again). Every turn pair records the closes its reads touched, taken from each read's own result and never from the model: a close-scoped read names the conversation's close (and the prior month's close when it compares with it; the budget comparison draws on months it cannot name, so its answer is shown once); a firm-wide list names the closes in its rows, unless it counted or capped beyond them or cannot name them at all (the morning brief, the track record, a search, a cut list), in which case the answer is **shown once and not kept**, in a close conversation as much as firm-wide: the saved reply is generic, a card proposed in that turn is dropped (ask for it on its own), the pair records no closes and is never replayed. Firm-wide, a close the model names is read only if you can open it now; the agent is told otherwise. Inside a close, the procedures read asks the route for this client's versions and the firm-wide ones (`GET /v1/closes/procedures?client=`), before the route's 200-version view limit, and is cut to them again on the way to the model. Before a conversation is shown or replayed, every pair that touched a close you can no longer open, or that no longer exists, is deleted, question and answer alike; deleting a close deletes every pair that named it. Only a kept answer with known lineage and its question are replayed (`replayable`); a pair that was not kept, or shown once, stays on screen until the conversation next advances, then is deleted, and never reaches a later request. The replayed window is bounded to 20 turns and about 24,000 characters, oldest out first. A card in a firm-wide conversation says which close it acts on. A firm-wide card's target close joins the pair's lineage and must be one you can open when it is proposed. The trace keeps each step's tool, outcome, size and link, never its arguments or content, and an answer may carry no link a read did not supply.

## Action cards

The agent proposes the same cards from its tools, with the arguments it chose kept on the message (`proposal`), and two more: **approval** (ask a person to review a draft, with the note the agent wrote; `POST …/workpapers/{id}/approval-request` on confirmation) **open_close** (open a client's month from the dashboard conversation: the card names the client and the month, is refused for a client the member is not assigned to, and on confirmation opens the close as the Clients page would and, in one transaction, makes it the card's close and part of the pair's lineage; the same month confirmed again returns the open close, `created: false`) and **portfolio** (run a goal on the closes `select_closes_preview` listed, each shown by client and month; confirmation previews the goal again, refuses a changed selection, saves the operation, checks the saved closes against the card once more, and runs its first batch of ten through `POST /v1/closes/portfolio/run` inside the request's deadline; while it is the newest card it keeps running a batch every few seconds until none is pending, shows each close's outcome (started, skipped, failed, pending) and links to Work, where the operation also continues). A card proposed in a firm-wide conversation names the close it acts on (`target_close_id`), is confirmed through `POST /v1/closes/chat/confirm/{message}`, and checks your access to that close at confirmation.

In a close conversation, a message that starts with **prepare**, **recheck** (or re-check) or **stop** and otherwise only names the close's work ("Prepare this close", "Recheck failed work", "Stop this close's unresolved work") answers with a card; "Prepare me for sign-off" or "Stop showing that" does not (migration 073). A firm-wide message that reads as a [portfolio goal](close-coordinator.md#portfolio-goals) ("prepare my clients due Friday", "hand over my clients due Friday") links to Work with the goal filled in, which previews the closes it selects.

The newest card in the conversation reads the close's preparation status (`GET /v1/closes/{id}/prepare-close`, every 5 seconds while it is shown) and lists exactly what the action covers: the source documents to prepare, with each source number and a note when no period ledger is linked or it is stale, or the named jobs to recheck or stop, with each job number (the status now names each job). It shows the latest preparation run with who started it and its jobs by state. When there is nothing to act on, or the exact scope is already being prepared, it says so and offers no confirmation.

Confirming needs the checkbox, the `workpapers:review` permission, and a 1–500 character reason to stop. The checkbox is tied to the list it was ticked against: any change to the list clears it, even if the list later changes back. `POST /v1/closes/{id}/chat/{message}/confirm` sends that list's digest and is refused if the list has changed. It then runs the close page's own route (`POST /prepare-close`, `/prepare-close/recheck` or `/prepare-close/cancel`) with your own permissions, and the route checks the digest again under its lock. Any refusal is shown as the route returned it, and nothing is recorded. For the same scope the prepare route returns the run already confirmed rather than start one, and now says whether it created the run; the card is then refused, since it started nothing. On success the card keeps the jobs it covered, the run it started (for prepare) and when. A card is confirmed at most once, and only by its author; while a confirmation runs, the assistant takes no other question. If the work started but the card could not record it, the card keeps saying so until the next confirmation.

Closing the assistant does not stop the work: the jobs run as they would from the close page. Reopening shows each of the card's jobs with its current state, any error or changed source, and links to its drafts, read from Work narrowed to those jobs (`GET /v1/closes/{id}/work?jobs=`, up to 100 job numbers; a card that stopped more points to Work for the rest). Older cards show only what they started.

### Request card

A message that starts with **draft** and otherwise names only a client request ("Draft a client request", "draft the request") answers with a request card (migration 090); "Draft the close" does not. The card reads `GET /v1/closes/{id}/requests/candidates`: the close's current drafts (no later draft revises them) that a consolidated request would ask about, oldest first and at most 20 (the rest are counted and chosen in Requests), because they have no sources, a changed source, blockers, or a requested change with a reviewer's note; and the exact wording [Requests](client-request-links.md) would save for them. Its checkbox is tied to one digest over those drafts, that wording and the latest saved request draft. Confirming (author only, once) reads the candidates again, refuses a changed digest or an empty list, and saves that wording through the Requests route's own freshness check, which refuses it if the evidence or reviews changed meanwhile. The card then says the draft was saved, or that the latest draft already has this wording, and links to Requests to edit, approve and deliver it. Nothing is approved or sent from the card.

## Executives

A client's executive gets the same panel over their own pages. Their conversation is firm-wide only (a close conversation is refused, as every close route is for an executive) and the agent offers exactly their reads: the business overview (`GET /v1/closes/executive`), a close's published reports and one report as frozen (its headline, or one section: Profit and Loss, balance sheet, working capital, cash flow or commentary), the 13-week cash plan and the budget comparison, naming a close by the id the overview gives. No card is offered and no command runs (`422`); the starter chips are the executive's own questions; citations link to the executive's pages. An executive asks outside a close, so only the firm-wide level (`firm`) sends their question to the model; at `closes` or `off` the executive gets one fixed line pointing to the business overview and no model is called, and that pair is never replayed to a model later. A cited report links to the version read (`?close=…&publication=…`), which the Reports page opens. An executive's conversation is its own (`chat_messages.executive`, migration 137): what the same person asked and was told as a member never shows and is never replayed once they are an executive, and the other way round.

## Search

`GET /v1/closes/search?q=` (across your closes) and `GET /v1/closes/{id}/search?q=` (one close) find literal matches of every word you type (3–100 characters, up to six words; more are refused), up to 8 of each kind (transactions: up to 8 from QuickBooks and 8 from Xero):

- **Documents** by name or extracted text, current versions only, including saved Stripe reports (each report is one document, not individual transactions); a client's reply is marked as a reply. Each links to the document on the close's Evidence tab.
- **Drafts**, current versions only, linked to the draft.
- **Transactions**: the supported ledger rows of each close's latest QuickBooks transaction capture whose evidence is still current, and of its newest Xero capture for the month (migration 089; the rows the close's exceptions read, so refunds, other payment types and open items are not included). Each shows the captured amount and payee and links to the QuickBooks ledger or Xero evidence on the Evidence tab; a Xero result's title starts with "Xero". A close's newest Xero capture of its month saved before this change gets its rows when the worker next starts, one capture at a time.
- **Procedures**: the current version of each enabled procedure for your clients or all clients.
- **Review decisions**: decision, reviewer, date and note, linked to the draft.

Only closes you can access are searched, in a read-only transaction with a time limit. Titles and snippets hide passwords, card, bank account and identity numbers like a client reply; the whole text is redacted before about 200 characters around the first word are shown. A match is a literal match, not a claim that a document supports or caused an entry; the answer says what was searched and that each kind is capped.

## Commands

Press **Cmd/Ctrl+K** anywhere in the workspace for the command palette, or type **/** in the assistant's box for the same commands. Commands run without a model at every Agent chat level: the panel sends the command itself (`{command, argument}`), the server runs the one fixed read or card it names and formats the reply in code, so a firm without the agent keeps precise controls and confirmed ad hoc actions. The starter chips send commands too.

| Command | Does |
| --- | --- |
| `/ask <question>` | Asks the agent in the current context, as free text typed without a slash would |
| `/status` | Where this close stands, or, outside a close, what needs you today |
| `/queue`, `/blocked`, `/delegated` | The review queue, blocked work, the closes handed to the agent, across your closes; typed in a close, the panel opens the firm conversation for them |
| `/explain [account]` | Whether that account ties, or what blocks sign-off |
| `/compare [account]` | What changed from last month, for that account or overall |
| `/find <words>` | Searches, in this close or across your closes |
| `/delegate [day]` | Asks for the card handing the close to the agent, ready for review by that day |
| `/prepare`, `/stop` | Asks for the close's prepare or stop card |
| `/request` | Asks for the close's [request card](#request-card) |
| `/review` | Opens the first draft in the review queue |
| `/rehearse`, `/export` | Opens the close's sign-off tab: the saved-proposal rehearsal and the package downloads |

A command that needs a close uses the one you are on. Elsewhere the palette lists your closes matching what you type after the command (for example `/prepare Acme`); picking one runs it there, and for `/explain` and `/compare` the chosen close becomes a chip and you then type the account. Typing such a command in the firm-wide assistant opens the palette to pick the close. The palette also finds commands by their descriptions, keeps focus inside it and returns it on close. In the assistant's box, Tab completes a command.

## Written answers and investigations

Replaced by the agent (above) in migration 135: a question is no longer sent with a fixed set of reads and checked by quotes alone; the agent chooses its reads and every figure is bound to a field. The firm policy is the one [Agent chat](#what-it-answers) level (Settings → Policies); the staff planner keeps its own consent (`staff_planning`, Settings → Policies). The track record under Human effort shows, for closes you can open, how many answers were asked and kept in the last 30 days and the typical time, and the firm's firm-wide counts.

Checks: `go test ./internal/transport -run 'TestAgentSupport|TestAgentChat'` with `TEST_DATABASE_URL`.

## Boundaries

The assistant reads and explains, and an agent answer is text bound to what it read. It never approves, sends, posts or signs off; the only work it starts is a card's action, after its author confirms the listed scope, through the close page's own route and checks. A hand-over card starts a [close goal](close-coordinator.md#hand-the-close-to-the-agent) the same way, and can set up a [monthly hand-over](close-coordinator.md#every-new-month); a firm-wide request that names a client is answered with a link to that close's card, shown once. If a read fails it says so rather than guessing. What you type is stored as written in your own conversation.

Checks: `go test ./internal/transport -run 'TestAssistant|TestChatAction'` with `TEST_DATABASE_URL`.
