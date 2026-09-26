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
| A draft | this draft | its review state, blockers and questions |
| Firm-wide | what needs you today | the morning brief |
| | the review queue | drafts ready for review |
| | blocked work | Work's blocked view |

A firm-wide answer shows its current details once, under the reply. History keeps only the generic reply and its link, so it never keeps an answer's client details you might later lose access to; your own questions are kept as you typed them. A close answer is kept in full, since the close conversation is reachable only with access to the close.

## Boundaries

The assistant reads and explains; it never approves, sends, posts, signs off or starts work, and it calls no POST route. If a read fails it says so rather than guessing. What you type is stored as written in your own conversation.

Checks: `go test ./internal/transport -run TestAssistant` with `TEST_DATABASE_URL`.
