# Assistant

The **Assistant** button (bottom right of every signed-in workspace page) opens a panel that answers questions from the saved work you can already see (migration 072). It shows only in a signed-in or unlocked workspace, and it remembers whether it was open in this browser; nothing else is kept in the browser.

## Context and history

- On a client close the conversation is about that close; its chip shows the client and month. On a draft, a second chip names the draft, and questions that name the draft or workpaper are about it; other questions stay about the close.
- Anywhere else the conversation is firm-wide.
- Each conversation belongs to the member who wrote it: nobody else reads it, including firm administrators. Switching to another close opens that close's conversation from empty; a question or answer never carries over to another client, and a late answer for the previous page is dropped.
- A close conversation is reachable only while you can access the close: firm membership and the client assignment are checked on every read and message. A draft must belong to the close.
- A conversation keeps and shows its latest 200 messages. **Clear** deletes it. Deleting the close deletes its conversations, and removing a member from the firm deletes theirs (a message being saved at that moment waits for the removal and is refused). Firm-wide messages older than 30 days are never shown, and the worker deletes them hourly.

## What it answers

Every answer comes from an existing read, made with your own permissions, and links to where the detail lives. No model is called: a question is matched to one of these by its words, and anything else gets a short list of what it can answer.

| Where | Ask about | From |
| --- | --- | --- |
| A close | where it stands | the close brief |
| | what blocks sign-off | sign-off readiness |
| | balances that don't tie, or a named account | the balance sheet tie-out |
| | what changed from last month, or a named account | changes from last month |
| | exceptions in the ledger capture | the Exceptions panel |
| | whether the ledger capture is certified | the close brief's ledger line |
| A close | find … (also search, look for, where is, show me) | search, in this close |
| A draft | this draft | its review state, blockers and questions |
| Firm-wide | find … | search, across your closes |
| | what needs you today | the morning brief |
| | the review queue | drafts ready for review |
| | blocked work | Work's blocked view |

A follow-up that refers back ("that", "it", "same") and names no account keeps the account of your previous question in the same close conversation, when that question was the same kind (a tie-out after a tie-out, a change after a change); otherwise nothing carries over. A question that refers back without naming what to read ("and that one?") asks the previous question's read again.

A firm-wide answer shows its current details once, under the reply. History keeps only the generic reply and its link, so it never keeps an answer's client details you might later lose access to; your own questions are kept as you typed them. A close answer is kept in full, since the close conversation is reachable only with access to the close.

## Search

`GET /v1/closes/search?q=` (across your closes) and `GET /v1/closes/{id}/search?q=` (one close) find literal matches of every word you type (3–100 characters, up to six words; more are refused), up to 8 of each kind:

- **Documents** by name or extracted text, current versions only; a client's reply is marked as a reply. Each links to the document on the close's Evidence tab.
- **Drafts**, current versions only, linked to the draft.
- **Transactions** in each close's latest QuickBooks transaction capture whose evidence is still current, shown with the captured amount and payee and linked to the QuickBooks ledger evidence on the Evidence tab; Xero records are not searched yet.
- **Procedures**: the current version of each enabled procedure for your clients or all clients.
- **Review decisions**: decision, reviewer, date and note, linked to the draft.

Only closes you can access are searched, in a read-only transaction with a time limit. Titles and snippets hide passwords, card, bank account and identity numbers like a client reply; the whole text is redacted before about 200 characters around the first word are shown. A match is a literal match, not a claim that a document supports or caused an entry; the answer says what was searched and that each kind is capped.

## Commands

Press **Cmd/Ctrl+K** anywhere in the workspace for the command palette, or type **/** in the assistant's box for the same commands. Commands run without a model: each asks the assistant one of the questions above or opens the screen that does the work, where every action keeps its preview and confirmation.

| Command | Does |
| --- | --- |
| `/ask <question>` | Asks the assistant in the current context |
| `/status` | Where this close stands, or, outside a close, what needs you today |
| `/explain [account]` | Whether that account ties, or what blocks sign-off |
| `/compare [account]` | What changed from last month, for that account or overall |
| `/find <words>` | Searches, in this close or across your closes |
| `/prepare`, `/stop` | Opens the close's preparation, with its preview, confirmation and stop |
| `/request` | Opens the close's client requests |
| `/review` | Opens the first draft in the review queue |
| `/rehearse`, `/export` | Opens the close's sign-off tab: the saved-proposal rehearsal and the package downloads |

A command that needs a close uses the one you are on. Elsewhere the palette lists your closes matching what you type after the command (for example `/prepare Acme`); picking one runs it there, and for `/explain` and `/compare` the chosen close becomes a chip and you then type the account. Typing such a command in the firm-wide assistant opens the palette to pick the close. The palette also finds commands by their descriptions, keeps focus inside it and returns it on close. In the assistant's box, Tab completes a command.

## Boundaries

The assistant reads and explains; it never approves, sends, posts, signs off or starts work, and it calls no POST route. If a read fails it says so rather than guessing. What you type is stored as written in your own conversation.

Checks: `go test ./internal/transport -run TestAssistant` with `TEST_DATABASE_URL`.
