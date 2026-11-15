# Tool input and output

Model telemetry cards are labelled **Model call statistics** when tokens, cost or timing are present, **Model stream event** for stream notifications, and **Model API event** otherwise. Imported Codex token reports retain **Token usage**. These labels do not imply that prompt or response text was recorded. Available conversation content appears as **User message**, **Assistant message**, **Model input** or **Model output** in the inspector.

Open a tool card to see the matching input or output at the top of the inspector. **Open output event** and **Open input event** switch between full event details and their original JSON, including when the counterpart is on another timeline page. Closing the inspector returns focus to the original card.

![Tool input and its output](ui-tool-pair.png)

Links use explicit call IDs, scoped to session, provider, executing agent and the selected origin. Paired import/OTEL representations count as one logical candidate. A link is shown only for a unique input and unique output; repeated/conflicting call IDs are not resolved by time proximity. Missing IDs or an uncollected counterpart are identified in the inspector. Events that already contain both sides retain their existing display.

The counterpart preview uses bounded cached cards. Its full original is fetched only when opened. This reuses the existing call-ID index and adds no historical backfill.

Codex events without a conversation ID can appear as `trace:codex:…` or `unassigned:codex`. These unassigned logs and traces are hidden from normal folder/session lists by default. Enable **Internal sessions** to inspect them. Original records remain stored and accessible, and a trace joins a real conversation when explicit trace ownership becomes available. A real conversation lacking working-directory metadata still belongs to **Folder unavailable**.

Codex-generated temporary sessions, such as automatic task-title generation, are also hidden by default. This rule requires both `session_init.ephemeral=true` and an `rpc.request_id` starting with `temporary-structured-`; explicit `ephemeral=false` keeps that rule from hiding a session. Ordinary ephemeral sessions remain visible.

Automatic approval assessments and next-message suggestions may have neither temporary attribute. Their dedicated application prompt templates are recognized separately. Approval assessments also require the `codex-auto-review` model and the structured approval-request delimiters. Suggestions require the explicit app-generated preface, composer description and output instructions. A model name or a general keyword alone never hides a session. If a session contains an ordinary user request as well, this template rule keeps it visible and titles it with the first real request. All logs remain available through **Internal sessions**, direct links, JSON export and search with internal sessions enabled. Existing user-message records are classified once in resumable background batches; visibility updates wait until every page has been examined.

Codex Companion Page open/snapshot envelopes are app-generated context too. Numbered `selected_page_part_N_of_M` envelopes require positive part numbers, a valid total and an exactly matching closing tag. They never supply a session title. A page-context-only session is hidden by default; mixed conversations use their first human request. Migration 0017 repairs existing titles, visibility and project counts in checkpointed background batches without changing original records.

```sh
AGENTLOG_BIN=./agentlog python3 scripts/tool_pairs_browser_test.py
```

Screenshots and checks use synthetic data.
