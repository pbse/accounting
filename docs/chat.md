# Assistant

The **Assistant** button (bottom right of every signed-in workspace page) opens a panel that answers questions from the saved work you can already see (migration 072). It shows only in a signed-in or unlocked workspace, and it remembers whether it was open in this browser; nothing else is kept in the browser.

## Context and history

- On a client close the conversation is about that close; its chip shows the client and month. On a draft, a second chip names the draft, and questions that name the draft or workpaper are about it; other questions stay about the close.
- Anywhere else the conversation is firm-wide.
- Each conversation belongs to the member who wrote it: nobody else reads it, including firm administrators. Switching to another close opens that close's conversation from empty; a question or answer never carries over to another client, and a late answer for the previous page is dropped.
- A close conversation is reachable only while you can access the close: firm membership and the client assignment are checked on every read and message. A draft must belong to the close.
- A conversation keeps and shows its latest 200 messages. **Clear** deletes it. Deleting the close deletes its conversations, and removing a member from the firm deletes theirs (a message being saved at that moment waits for the removal and is refused). Firm-wide messages older than 30 days are never shown, and the worker deletes them hourly.

## What it answers

Every answer comes from an existing read, made with your own permissions, and links to where the detail lives. A question is matched to one of these by its words without a model, and anything else gets a short list of what it can answer, unless the firm has turned on [written answers](#written-answers).

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
| | delegated closes, or what the agent is working on, asked plainly ("how are my delegated closes doing?") | **Handed to the agent**: how many, and the first ten in Work's words (with when the agent last looked, once that is over ten minutes ago), due dates and who handed them over. A more specific question about them ("which delegated closes are blocked?") goes to written answers when they are on, and never to a list of other work |

A follow-up that refers back ("that", "it", "same") and names no account keeps the account of your previous question in the same close conversation, when that question was the same kind (a tie-out after a tie-out, a change after a change); otherwise nothing carries over. A question that refers back without naming what to read ("and that one?") asks the previous question's read again.

A firm-wide answer shows its current details once, under the reply. History keeps only the generic reply and its link, so it never keeps an answer's client details you might later lose access to; your own questions are kept as you typed them. A close answer is kept in full, since the close conversation is reachable only with access to the close.

## Action cards

In a close conversation, a message that starts with **prepare**, **recheck** (or re-check) or **stop** and otherwise only names the close's work ("Prepare this close", "Recheck failed work", "Stop this close's unresolved work") answers with a card; "Prepare me for sign-off" or "Stop showing that" does not (migration 073). A firm-wide message that reads as a [portfolio goal](close-coordinator.md#portfolio-goals) ("prepare my clients due Friday", "hand over my clients due Friday") links to Work with the goal filled in, which previews the closes it selects.

The newest card in the conversation reads the close's preparation status (`GET /v1/closes/{id}/prepare-close`, every 5 seconds while it is shown) and lists exactly what the action covers: the source documents to prepare, with each source number and a note when no period ledger is linked or it is stale, or the named jobs to recheck or stop, with each job number (the status now names each job). It shows the latest preparation run with who started it and its jobs by state. When there is nothing to act on, or the exact scope is already being prepared, it says so and offers no confirmation.

Confirming needs the checkbox, the `workpapers:review` permission, and a 1–500 character reason to stop. The checkbox is tied to the list it was ticked against: any change to the list clears it, even if the list later changes back. `POST /v1/closes/{id}/chat/{message}/confirm` sends that list's digest and is refused if the list has changed. It then runs the close page's own route (`POST /prepare-close`, `/prepare-close/recheck` or `/prepare-close/cancel`) with your own permissions, and the route checks the digest again under its lock. Any refusal is shown as the route returned it, and nothing is recorded. For the same scope the prepare route returns the run already confirmed rather than start one, and now says whether it created the run; the card is then refused, since it started nothing. On success the card keeps the jobs it covered, the run it started (for prepare) and when. A card is confirmed at most once, and only by its author; while a confirmation runs, the assistant takes no other question. If the work started but the card could not record it, the card keeps saying so until the next confirmation.

Closing the assistant does not stop the work: the jobs run as they would from the close page. Reopening shows each of the card's jobs with its current state, any error or changed source, and links to its drafts, read from Work narrowed to those jobs (`GET /v1/closes/{id}/work?jobs=`, up to 100 job numbers; a card that stopped more points to Work for the rest). Older cards show only what they started.

### Request card

A message that starts with **draft** and otherwise names only a client request ("Draft a client request", "draft the request") answers with a request card (migration 090); "Draft the close" does not. The card reads `GET /v1/closes/{id}/requests/candidates`: the close's current drafts (no later draft revises them) that a consolidated request would ask about, oldest first and at most 20 (the rest are counted and chosen in Requests), because they have no sources, a changed source, blockers, or a requested change with a reviewer's note; and the exact wording [Requests](client-request-links.md) would save for them. Its checkbox is tied to one digest over those drafts, that wording and the latest saved request draft. Confirming (author only, once) reads the candidates again, refuses a changed digest or an empty list, and saves that wording through the Requests route's own freshness check, which refuses it if the evidence or reviews changed meanwhile. The card then says the draft was saved, or that the latest draft already has this wording, and links to Requests to edit, approve and deliver it. Nothing is approved or sent from the card.

## Search

`GET /v1/closes/search?q=` (across your closes) and `GET /v1/closes/{id}/search?q=` (one close) find literal matches of every word you type (3–100 characters, up to six words; more are refused), up to 8 of each kind (transactions: up to 8 from QuickBooks and 8 from Xero):

- **Documents** by name or extracted text, current versions only, including saved Stripe reports (each report is one document, not individual transactions); a client's reply is marked as a reply. Each links to the document on the close's Evidence tab.
- **Drafts**, current versions only, linked to the draft.
- **Transactions**: the supported ledger rows of each close's latest QuickBooks transaction capture whose evidence is still current, and of its newest Xero capture for the month (migration 089; the rows the close's exceptions read, so refunds, other payment types and open items are not included). Each shows the captured amount and payee and links to the QuickBooks ledger or Xero evidence on the Evidence tab; a Xero result's title starts with "Xero". A close's newest Xero capture of its month saved before this change gets its rows when the worker next starts, one capture at a time.
- **Procedures**: the current version of each enabled procedure for your clients or all clients.
- **Review decisions**: decision, reviewer, date and note, linked to the draft.

Only closes you can access are searched, in a read-only transaction with a time limit. Titles and snippets hide passwords, card, bank account and identity numbers like a client reply; the whole text is redacted before about 200 characters around the first word are shown. A match is a literal match, not a claim that a document supports or caused an entry; the answer says what was searched and that each kind is capped.

## Commands

Press **Cmd/Ctrl+K** anywhere in the workspace for the command palette, or type **/** in the assistant's box for the same commands. Commands run without a model: each asks the assistant one of the questions above, or opens the screen that does the work, where every action keeps its preview and confirmation.

| Command | Does |
| --- | --- |
| `/ask <question>` | Asks the assistant in the current context |
| `/status` | Where this close stands, or, outside a close, what needs you today |
| `/explain [account]` | Whether that account ties, or what blocks sign-off |
| `/compare [account]` | What changed from last month, for that account or overall |
| `/find <words>` | Searches, in this close or across your closes |
| `/delegate [day]` | Asks for the card handing the close to the agent, ready for review by that day |
| `/prepare`, `/stop` | Asks for the close's prepare or stop card |
| `/request` | Asks for the close's [request card](#request-card) |
| `/review` | Opens the first draft in the review queue |
| `/rehearse`, `/export` | Opens the close's sign-off tab: the saved-proposal rehearsal and the package downloads |

A command that needs a close uses the one you are on. Elsewhere the palette lists your closes matching what you type after the command (for example `/prepare Acme`); picking one runs it there, and for `/explain` and `/compare` the chosen close becomes a chip and you then type the account. Typing such a command in the firm-wide assistant opens the palette to pick the close. The palette also finds commands by their descriptions, keeps focus inside it and returns it on close. In the assistant's box, Tab completes a command.

## Written answers

Written answers are off until a firm administrator (`org:manage`; anyone in local development) turns them on in Firm settings, with a reason that is kept in the firm's policy history (migration 087). While they are on, a question in a client close that none of the reads above matches (and that is not a card, hand-over or find request) is sent to OpenAI with that close's reads, made with your own permissions: the close brief, sign-off readiness (only its state, blockers and scope), the balance sheet tie-out, changes from last month, the ledger exceptions and, on a draft, the draft's kind, review state and content. Each read is sent whole or not at all: one over 24 KiB is named as omitted and one that failed as unavailable, so the model never takes part of a list for all of it. The question and each text value in the reads first have card, IBAN and Social Security numbers, labelled bank account numbers and sign-in details removed (a read that is not valid JSON, or a trimmed read that is not a JSON object, is treated as unavailable); the request asks OpenAI not to store it, frames the reads as untrusted data, gives the model no tools (unless [investigations](#investigations) are on) and allows it only an answer and, for each read it relies on, a quote copied from that read. The answer is kept only if every read it cites was sent and holds its quote; otherwise, or if the model says it cannot answer, the help reply stays. A kept answer links the cited reads' pages (never a link the model wrote) and ends by saying it was written by the model. Each attempt counts against the firm's daily model limit, whether or not an answer is kept, and at the limit the help reply says so. The assistant's note says what is sent if written answers are on, and the [security center](operations.md#security-center) shows whether they are. Firm-wide questions need a second, separate setting, **firm-wide answers** (migration 093), also off until an administrator turns it on with a reason, because firms that turned on written answers were told firm-wide questions are never sent. While it is on, a firm-wide question that none of the firm-wide reads matches (and that is not a goal, hand-over or find request) is sent the same way with four reads made with your own permissions: what needs a person today, the review queue, blocked work and the closes handed to the agent (with what the agent last found on each), so a question across clients such as "which delegated closes are due this week and still waiting on the client?" can be answered. The model is told that a list may be capped and that it must not answer as if a capped list were complete. The answer streams and is checked the same way, is then shown once as lines under a generic reply with links to Work, and is never stored, so firm-wide history still keeps no client details. Before the first streamed line and again before saving, the clients you can open must be the ones the reads were made with; otherwise you get 403 and nothing is saved. Firm-wide attempts count against the daily model limit but are not measured in the track record. The answer streams as the model writes it, in a bubble marked as not yet checked, and only after the model has said the question is answerable; before the first line your firm membership and close access are checked again. When the complete answer fails its checks, the bubble is replaced by the help reply, which says the written answer was withdrawn, and the withdrawn text is not stored. Each attempt records only its outcome (kept, not kept, unanswerable, failed or at the limit) and how long it took, never the question or answer (migration 092); the agent track record under Human effort shows, for closes you can open, how many were asked and kept in the last 30 days and the typical time to write one. The records are removed with the close and with a removed member's conversations.

Checks: `go test ./internal/transport -run TestAssistantWritesAnswersOnlyWhenTheFirmAllows` with `TEST_DATABASE_URL`.

## Investigations

With **investigations** on (a third firm setting, off until an administrator turns it on with a reason, only while written answers are on, and turned off with them; migration 105), a close question that goes to the model is first investigated: the model may call the assistant's read-only tools for that close, at most six reads over four model rounds within 25 seconds, each round counted against the firm's daily model limit. The tools are the MCP adapter's close-scoped reads (the close, its documents and a document's extracted text, a draft and its changes, the tie-out, changes from last month, exceptions, readiness, the brief, Xero's reconciled flags, the proposed-state preview, the close's jobs, request drafts and the preparation preview) and a literal search within the close; every read is made through the close's own routes with your permissions, and the close is always the conversation's, whatever the model sends. Each read's result is redacted by what the tool returns, never by what the body looks like (a document's text as one text, whatever it parses as; every other read as a JSON value that must fill its body, value by value), like the fixed reads, and sent whole or not at all (over 24 KiB it is named as omitted; a failed read as unavailable; a read the assistant does not offer, or with arguments that do not fit, is refused and the model told so). The reads then join the fixed reads as sources the final answer must quote from, checked exactly as before, and a kept answer links each document or draft it read and starts with **Investigated:** naming each read and its outcome. The trace (tool, arguments as sent, outcome, size and time; never the content) is kept with the attempt's record. The model reads only: nothing here changes, approves, sends or posts, and it prepares no draft; a supported adjustment still goes through the confirm form or a straight-through draft.

Checks: `go test ./internal/transport -run TestAssistantInvestigatesBeforeWriting` with `TEST_DATABASE_URL`.

## Boundaries

The assistant reads and explains, and a written answer is text only. It never approves, sends, posts or signs off; the only work it starts is a card's action, after its author confirms the listed scope, through the close page's own route and checks. A hand-over card starts a [close goal](close-coordinator.md#hand-the-close-to-the-agent) the same way, and can set up a [monthly hand-over](close-coordinator.md#every-new-month); a firm-wide request that names a client is answered with a link to that close's card, shown once. If a read fails it says so rather than guessing. What you type is stored as written in your own conversation.

Checks: `go test ./internal/transport -run 'TestAssistant|TestChatAction'` with `TEST_DATABASE_URL`.
