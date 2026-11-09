# herrscher-jev-compactor

**A compaction stage that keeps what still matters.** Before the backend of a
session summarises its conversation, this stage asks
Jev, the System One model of TypeSafe, which tool calls
the assistant still needs. The others are pruned from the history the backend
keeps, verbatim everywhere else: no summary is written, no message is
rephrased.

It is a Go port of
[fast-jev-compaction](https://github.com/tamaratran/fast-jev-compaction).

Status: live.

## What it is, in the plugin model

A `contracts.Plugin` of category `compactor`, kind `jev`, built through its
`CompactorFactory`. It declares `Capabilities.Compactor`. It is not a backend:
it answers no prompt and never owns a session.

It needs the session's backend to edit its history in place, which a backend
declares with `Capabilities.HistoryEditor`, and a host that hands the stage a
`contracts.HistoryEditor` in `CompactRequest.History`. Two releases are
needed for that: herrscher-claude-backend v0.12.0 or later, the first backend
to edit its history, and herrscher v0.50.0 or later, the first host to fill
`CompactRequest.History` and to load compactor plugins named in
`compactStack`. With an older backend the stage does nothing, returns its
`no_history` note, and lets the backend compact.

## Install

Blank-import the package into a herrscher host:

```go
import _ "github.com/Herrscherd/herrscher-jev-compactor"
```

Then list it ahead of the backend's own compaction in `config.json`:

```json
{ "compactStack": ["jev", "backend"] }
```

and give it a TypeSafe API key:

```bash
export TYPESAFE_API_KEY=...
```

Without a key the host refuses to build the stage and names the missing
setting.

## What one compaction does

1. Read the history through `ReadHistory`.
2. Pin the calls of the first message and of the newest `preserve_recent`
   messages. They are never asked about and never pruned. A call without a
   result yet is not a candidate either. `preserve_recent` counts
   `HistoryMessage` values, not turns of the backend: the `claude` backend
   folds the tool results of a turn into the assistant message that made the
   calls, so a message holds a call and its result together.
3. Build the state Jev reads: the whole conversation, oldest first, with every
   tool result replaced by a short note. A state over `max_state_tokens` is
   shrunk in stages until it fits: tool inputs cut to 1000, 200, then 60
   characters, long texts abridged (old messages first), old messages collapsed
   to a one-line note, old calls folded to one line each, old messages without
   calls left out, then runs of old call-only messages merged. A history that
   still does not fit stops the stage with `ErrHistoryTooLarge`.
4. Ask two questions per unpinned call: should the call stay, and should its
   full output stay. Questions are batched so state and questions fit
   `max_request_tokens`, and batches run in parallel, four at most.
5. Decide per call, against `keep_call_threshold` and `keep_result_threshold`
   (both default to `keep_threshold`), or the thresholds of the call's rule:
   - the output is still needed: keep the call untouched;
   - only the call is: keep its first `truncate_head` and its last
     `truncate_tail` characters of output (`PruneResult`), when the output is
     longer than those by more than 120;
   - neither: prune the call (`PruneCall`), which keeps the call in place with
     its input shortened and its output replaced by a note.
6. Apply every edit in one `PruneHistory` call.
7. Report `Done` when the history shrank by at least `min_reduction`, so the
   backend's own compaction runs only when pruning was not enough. The share
   is measured against the whole history, message text included, not only
   against the tool inputs and outputs the pruning touches.

`reduction_unit` sets the unit the share is measured in. With `characters`, the
default, the share counts the characters of the tool inputs and outputs, and
the message text, as above. With `tokens`, it counts the tokens estimated as
described below, of the same parts, and the reduction is the plan's estimate
(the tokens it removes over the tokens of the history): `PruneOutcome` only
reports characters, so the share cannot come from the backend in that unit.
`max_removed_share` stays in characters.

When `on_short` is `revert`, a pruning that falls short is not applied at all.
Before Jev is asked, the stage takes the most pruning could remove, every
unpinned call with its input and output, and if that stays under
`min_reduction` it does not ask Jev and leaves the history as it was. After Jev
answers, it measures the share the plan removes, the text it takes out, and
calls `PruneHistory` only when that share reaches the target. The plan measure
is an estimate: the backend writes a note in place of each pruned output and
shortens the inputs, so the real share can differ slightly from it. When the
edits are applied and still fall short, they stay applied and the note reports
the short reduction, as `keep` does.

### Rules, pins and budgets

`pin_inputs` keeps, without asking Jev, every call whose input contains one of
its substrings, and every output shorter than `min_result_chars`. The newest
messages and the first message stay pinned as before.

`rules` sets what happens to the calls of a given tool, without asking Jev for
them. A rule is `motif[,motif]:action[,action]`, rules are separated by `;`,
and the first rule whose motif matches the tool name wins. A motif is a
`path.Match` glob such as `Read*`. The actions are `keep` (the call stays
untouched), `prune` (the call is pruned, as `PruneCall`) and `truncate` (the
output keeps its first `head` and last `tail` characters, as `PruneResult`). A
rule with no action leaves
the decision to Jev, judged against the thresholds it sets with `threshold=`,
`call_threshold=` and `result_threshold=`, and against its `head` and `tail`.
For example:

```bash
export JEV_RULES='Edit,Write:keep;Bash:threshold=0.7,head=500;Grep:prune'
```

A call decided by a rule with an action is never asked about, and when a budget
is exceeded such a decision is the last to be given back.

`max_pruned` and `max_removed_share` cap one compaction. When the plan would
prune more calls than `max_pruned`, or remove more than `max_removed_share` of
the history, the pruning decisions carrying the highest keep probability (the
ones Jev is least sure of) are demoted to keep first, until the plan fits.
Decisions taken by a rule are demoted last.

The goal Jev weighs each call against is the operator's instruction
(`/compact <instruction>`), or the last three prompts of the user without one.

When Jev fails (a refused status, a redirect, a malformed body, an
unanswered question, a network error or a timeout), or when the history leaves
no room for Jev (`ErrHistoryTooLarge`, `ErrNoRoomForQuestions`), nothing is
pruned and the stage steps aside: it logs a warning, returns the `failed` note
with the reason, a `CodeCompactUnavailable` notice and `Done` false, and the
next stage of the stack compacts, as fast-jev falls back to the built-in
summary. The reason never carries the API key: an error body that echoes it is
redacted. The compaction fails only where going on would be wrong: when the
history cannot be read or pruned, or when its context is cancelled.

A `429` or a `5xx` answer is sent again up to `retries` times, after a wait of
`retry_backoff` doubled at each retry, or of the `Retry-After` the answer gives
when that is longer (in seconds or as an HTTP date). Waits are capped at 30s:
a `Retry-After` over 30s is not waited for, the failure stands and the stage
steps aside as above. Other `4xx` answers, network errors and timeouts are
never retried, and a wait ends with the context of the compaction.

`max_requests` and `max_sent_tokens` cap what one compaction sends to Jev, retries
included (the tokens are the same estimate `max_request_tokens` uses). Each
attempt is counted before it is sent, so the cap holds across parallel batches.
A batch that would go past the cap is not sent: its calls are kept, never pruned
by default, and the note says the cap was reached with the requests and tokens
actually sent. The decisions already taken still apply, and the stage is
`skipped` when nothing was pruned. `0` (the default) means no cap. The Stats
report the requests and tokens sent, whatever the cap.

`decision_log` names a JSONL file where every decision on a tool call is appended,
one line per decision with the fields of the debug journal (`ref`, `tool`,
`verdict`, `reason`, `result_size`, and the probabilities and thresholds when Jev
decided). The file is opened in append mode and created with mode `0600`; an
existing file keeps the mode it has. A file that cannot be opened or written is
reported in the logs and the compaction goes on without it.

## Settings

| Key | Env | Default | Meaning |
|---|---|---|---|
| `api_key` | `TYPESAFE_API_KEY` | required | TypeSafe API key |
| `model` | `JEV_MODEL` | `jev-latest` | Jev model asked |
| `endpoint` | `JEV_ENDPOINT` | `https://api.typesafe.ai/v1/systemone` | System One URL, https only |
| `keep_threshold` | `JEV_KEEP_THRESHOLD` | `0.5` | probability, 0 to 1, at or above which a call or its output stays |
| `keep_call_threshold` | `JEV_KEEP_CALL_THRESHOLD` | `keep_threshold` | probability, 0 to 1, at or above which a call stays |
| `keep_result_threshold` | `JEV_KEEP_RESULT_THRESHOLD` | `keep_threshold` | probability, 0 to 1, at or above which the full output stays |
| `rules` | `JEV_RULES` | none | per-tool actions, see below |
| `pin_inputs` | `JEV_PIN_INPUTS` | none | comma-separated substrings: a call whose input contains one always stays |
| `keep_errors` | `JEV_KEEP_ERRORS` | `false` | `true` keeps the output of a failed call, so a pruned call keeps its error |
| `min_result_chars` | `JEV_MIN_RESULT_CHARS` | `0` | outputs shorter than this many characters always stay |
| `max_pruned` | `JEV_MAX_PRUNED` | `0` | most tool calls one compaction prunes, `0` for no limit |
| `max_removed_share` | `JEV_MAX_REMOVED_SHARE` | `1` | most of the history, 0 to 1, one compaction may remove, `1` for no limit |
| `preserve_recent` | `JEV_PRESERVE_RECENT` | `6` | newest messages whose calls are pinned |
| `truncate_head` | `JEV_TRUNCATE_HEAD` | `300` | characters kept from an output Jev lets go |
| `truncate_tail` | `JEV_TRUNCATE_TAIL` | `0` | characters kept from the end of an output Jev lets go |
| `min_reduction` | `JEV_MIN_REDUCTION` | `0.25` | share of the history, 0 to 1, that stops the stack |
| `reduction_unit` | `JEV_REDUCTION_UNIT` | `characters` | unit `min_reduction` is measured in: `characters`, or `tokens` estimated as below |
| `max_state_tokens` | `JEV_MAX_STATE_TOKENS` | `25000` | token budget of the state |
| `max_request_tokens` | `JEV_MAX_REQUEST_TOKENS` | `30000` | token budget of one request |
| `timeout` | `JEV_TIMEOUT` | `2m0s` | timeout of one request, as a Go duration |
| `retries` | `JEV_RETRIES` | `0` | times a request is sent again after a 429 or a 5xx answer, at most 10, `0` for none |
| `retry_backoff` | `JEV_RETRY_BACKOFF` | `1s` | first wait before a retry, as a Go duration, doubled at each retry and capped at 30s |
| `max_requests` | `JEV_MAX_REQUESTS` | `0` | most requests sent to Jev for one compaction, retries included, `0` for no cap |
| `max_sent_tokens` | `JEV_MAX_SENT_TOKENS` | `0` | most tokens sent to Jev for one compaction, retries included and estimated, `0` for no cap |
| `decision_log` | `JEV_DECISION_LOG` | empty | JSONL file where each decision on a tool call is appended, empty for none |
| `max_parallel` | `JEV_MAX_PARALLEL` | `4` | requests sent to Jev at once, at least 1 |
| `on_short` | `JEV_ON_SHORT` | `keep` | `keep` leaves a short reduction to the next stage, `done` stops the stack there, `revert` leaves the history as it was when the reduction is short |
| `dry_run` | `JEV_DRY_RUN` | `false` | `true` computes what Jev would prune and changes nothing |
| `state_input_runes` | `JEV_STATE_INPUT_RUNES` | `1000,200,60` | three cut levels of tool inputs in the state, longest first: full, short, line |
| `state_text_head` | `JEV_STATE_TEXT_HEAD` | `400` | characters kept at the head of a long text when the state abridges it |
| `state_text_tail` | `JEV_STATE_TEXT_TAIL` | `150` | characters kept at the tail of a long text when the state abridges it |
| `state_context` | `JEV_STATE_CONTEXT` | built-in text | context text Jev reads, replacing the built-in one when set |
| `state_stages` | `JEV_STATE_STAGES` | all, in order | ordered reduction stages allowed among `abridge_texts`, `collapse_messages`, `compact_calls`, `leave_out`, `merge_calls` |
| `goal_source` | `JEV_GOAL_SOURCE` | `auto` | `auto` takes the instruction, then the last user prompts; `prompts` always takes the last user prompts; `fixed` always takes `goal_text` |
| `goal_text` | `JEV_GOAL_TEXT` | none | the fixed goal, required when `goal_source` is `fixed` |
| `goal_prompts` | `JEV_GOAL_PROMPTS` | `3` | number of the last user prompts that make the goal, at least 1 |
| `goal_prompt_runes` | `JEV_GOAL_PROMPT_RUNES` | `500` | characters kept of each user prompt in the goal, at least 1 |
| `preserve_first` | `JEV_PRESERVE_FIRST` | `true` | `true` keeps the first message of the history, the initial request, never pruned or abridged |
| `preserve_recent_tokens` | `JEV_PRESERVE_RECENT_TOKENS` | `0` | token budget of the newest messages whose tool calls are never pruned, `0` for none |
| `preserve_turns` | `JEV_PRESERVE_TURNS` | `0` | everything after the Nth last user message is never pruned, `0` for none |

Tokens are estimated without a tokenizer, with the weights fast-jev calibrated
against what Jev reports: a word costs one token per six letters, a digit half
a token, any other symbol nine tenths. Symbols are counted in UTF-16 units
and spaces are the JavaScript `\s` set, as in fast-jev, so a character
outside the Basic Multilingual Plane costs two symbols.

## Health

The stage answers the host's health probe (`Plugin.Probe`) without building a
live stage and without compacting anything. The key is read from the plugin
config first, then from `TYPESAFE_API_KEY`, as the factory reads it.

- No key: Down, with the remedy of adding `TYPESAFE_API_KEY` to the service
  environment and restarting the service.
- Key present: one authenticated request to the endpoint with no question and
  no history, so Jev prunes nothing. A 401 or 403 is Down (credentials
  refused), a 429 is Degraded (rate limited), a timeout or a 5xx is Degraded
  (unreachable), a connection failure, a redirect or a 404 is Down
  (unreachable), and any other answer is OK with the latency of the request.

The reason shown for an unreachable endpoint is a short cause such as
`timeout` or `http 503`; the key never appears in a notice. `Deep` changes
nothing: the request is the same with or without it, and it asks Jev nothing.

## Develop

```bash
scripts/check.sh
```

runs the quality chain of the go-book: tidy, format, golangci-lint, the
comment check, tests with `-race`, build and govulncheck.

## License

MIT.
