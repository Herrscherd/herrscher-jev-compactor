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

The note Jev returns ends with the state it was given: its size in tokens and
the reduction stage that made it fit, so an operator can see when the state
had to be cut.
6. Apply every edit in one `PruneHistory` call.
7. Report `Done` when the history shrank by at least `min_reduction`, so the
   backend's own compaction runs only when pruning was not enough. The share
   is measured against the whole history, message text included, not only
   against the tool inputs and outputs the pruning touches.

`reduction_unit` sets the unit the share is measured in. With `characters`, the
default, the share counts the characters of the tool inputs and outputs, and
the message text, as above, and comes from the backend's `PruneOutcome`. With
`tokens`, it counts the tokens estimated as described below, of the same parts,
on the history read again after the edits. `max_removed_share` stays in
characters.

When `on_short` is `revert`, a pruning that falls short is not applied at all.
Before Jev is asked, the stage takes the most pruning could remove, every
unpinned call with its input and output, and if that stays under
`min_reduction` it does not ask Jev and leaves the history as it was. After Jev
answers, it measures the share the plan removes, net of the note the backend
writes in place of each pruned output, and calls `PruneHistory` only when that
share reaches the target. The plan does not model the shortening of the inputs
of pruned calls, so the real share is at least the plan's, never less. When the
edits are applied and still fall short, they stay applied and the note reports
the short reduction, as `keep` does. `on_short` is read the same way in a dry
run, which then reports the revert instead of the projection.

### Rules, pins and budgets

`pin_inputs` keeps, without asking Jev, every call whose input contains one of
its substrings. `min_result_chars` keeps, the same way, every call whose output
is shorter than that many characters. The newest messages and the first message
stay pinned as before.

`rules` sets what happens to the calls of a given tool, without asking Jev for
them. A rule is `motif[,motif]:action[,action]`, rules are separated by `;`,
and the first rule whose motif matches the tool name wins. A motif is a
`path.Match` glob such as `Read*`. The actions are `keep` (the call stays
untouched), `prune` (the call is pruned, as `PruneCall`) and `truncate` (the
output keeps its first `head` and last `tail` characters, as `PruneResult`).
`head=0` keeps none of the output's head. In the wording of the issue, `keep`
is `always_keep` (a matching call is always kept) and `prune` is `never_keep`
(a matching call is never kept). A rule with no action leaves the
decision to Jev, judged against the thresholds it sets: `threshold=` sets both
channels, `call_threshold=` and `result_threshold=` set one each and win over
`threshold=` whatever the order, and each is given at most once. A rule
also takes its `head` and `tail`.
For example:

```bash
export JEV_RULES='Edit,Write:keep;Bash:threshold=0.7,head=500;Grep:prune'
```

A call decided by a rule with an action is never asked about, and when a budget
is exceeded such a decision is the last to be given back.

`max_pruned` and `max_removed_share` cap one compaction. When the plan would
edit more calls than `max_pruned`, or remove more than `max_removed_share` of
the history, the decisions carrying the highest keep probability (the ones Jev
is least sure of) are demoted to keep first, until the plan fits. `max_pruned`
counts every edited call, truncated outputs included, not only the pruned ones.
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
when that is longer (in seconds or as an HTTP date). A wait is never longer
than 30s: a `Retry-After` over 30s stops the retries, nothing waits for it, the
failure stands and the stage steps aside as above. Other `4xx` answers, network errors and timeouts are
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
reported in the logs and the compaction goes on without it. Every process writing
the same path takes an exclusive lock on `<file>.lock`, set to mode `0600` each
time it is opened and left on disk after the compaction.

A tool call whose result is empty is not judged by default, and so is never pruned,
because the history gives no way to tell a call still running from one that
answered with nothing. `include_empty_results=true` judges those calls too, so a
call still running can be pruned or truncated before it returns. Keep it off unless
the calls with an empty result are the ones to reduce.

`incremental=true` keeps a cache of the calls Jev judged to keep, so a later
compaction of the same session does not send them again. The cache is scoped to
the session name and incarnation, and each entry is keyed by the call ID and a
digest of the goal, the tool, the input, the result and the status. A call whose
content changes, or a compaction whose goal changes, is judged again. Only the
verdicts to keep are stored: a call Jev wants to prune is asked about again on
every compaction. The cache lives in memory of the plugin
process, is bounded to 4096 calls, and is emptied when it would grow past that
bound. Nothing is written to disk. When calls are answered from the cache, the
note says how many, as in `2 calls kept from the cache, not asked again.`

### Capture and replay

`replay_dir` names a directory where each compaction writes one JSON snapshot:
the history as Jev read it, the goal, the probabilities Jev gave for each call it
judged, the calls the incremental cache kept without asking, and the effective
settings, without the API key. The snapshot holds the full history in clear,
tool outputs included, so whatever the session read or wrote (files, command output,
messages) is in the file, personal data and secrets among it. Files are created with mode `0600`, so only the owner of the directory can
read them. Keep the directory out of shared storage and out of version control.

The directory is created with mode `0700` when it does not exist. An existing
directory that gives group or other any access is refused with a warning and
nothing is written: the directory is never chmod-ed. A snapshot is at most 64 MiB
once encoded; a larger one is refused with a warning and nothing is written. The
files are unique per compaction (`compaction-<UTC time>-<random>.json`). A
snapshot that cannot be written is reported in the logs, its partial file removed,
and the compaction goes on. Empty, the default, writes nothing.

`Replay` takes a snapshot and a settings map and returns the plan the stage would
have made with those settings, without any network call. It shares the decision
code of the compaction, including the rule that a plan with nothing to prune or
a short reduction is not applied. A call with no recorded probability is kept and
counted as unanswered rather than guessed. When `incremental` is on, the calls the
capture recorded as served by the cache are kept and counted in the `cached`
column, not as unanswered; with `incremental` off they are counted as unanswered
like any other call Jev did not judge.

### Bench

`cmd/jev-bench` replays one or more snapshots under the recorded settings and under
each settings file, then prints what each setting would have done:

```bash
go run ./cmd/jev-bench -settings strict.settings -settings loose.settings compaction-*.json
go run ./cmd/jev-bench -lang fr -json -settings strict.settings session.json
```

A settings file holds `key=value` lines, blank lines skipped, laid over the recorded
settings of each snapshot. A settings file is at most 64 KiB and must be a regular
file. The column `recorded` is the settings the snapshots were captured under, and
each settings file is named by its base name. The table gives per setting the calls
kept, truncated, pruned, unanswered, cached and reverted, and the reduction in
characters and in estimated tokens. `-json` prints the same figures as JSON, with
English keys whatever the language. `-lang en|fr` picks the language of the table,
the help and the messages: `en` by default, any other value is a usage error. The
bench never calls Jev, never reads `TYPESAFE_API_KEY` and refuses `api_key` in a
settings file. Its messages never echo the value of `api_key` or `endpoint`; for a
malformed line they give the file and the line number. The messages of the command
are translated; the detail of an error coming from the flag package, the system or
Jev stays in English.

Exit codes:

| Code | Meaning |
|---|---|
| `0` | the comparison was printed, or the help was asked for |
| `2` | the command line is wrong |
| `65` | a snapshot is malformed, has another version or fails a check |
| `66` | a snapshot or settings file does not exist or is not a regular file |
| `74` | output or a file could not be read or written |
| `78` | a settings file is malformed, too large or sets a setting jev refuses |

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
| `include_empty_results` | `JEV_INCLUDE_EMPTY_RESULTS` | `false` | `true` also judges calls whose result is empty, which can include calls still running |
| `min_result_chars` | `JEV_MIN_RESULT_CHARS` | `0` | outputs shorter than this many characters always stay |
| `max_pruned` | `JEV_MAX_PRUNED` | `0` | most tool calls one compaction edits, pruned or truncated, `0` for no limit |
| `max_removed_share` | `JEV_MAX_REMOVED_SHARE` | `1` | most of the history, above 0 up to 1, one compaction may remove, `1` for no limit; measured in `reduction_unit`; refused when `min_reduction` is above it, in either unit |
| `preserve_recent` | `JEV_PRESERVE_RECENT` | `6` | newest messages whose calls are pinned |
| `truncate_head` | `JEV_TRUNCATE_HEAD` | `300` | characters kept from an output Jev lets go, `0` for none |
| `truncate_tail` | `JEV_TRUNCATE_TAIL` | `0` | characters kept from the end of an output Jev lets go; needs a backend honoring `KeepTail`, herrscher-claude-backend v0.14.0 or later |
| `min_reduction` | `JEV_MIN_REDUCTION` | `0.25` | share of the history, 0 to 1, that stops the stack |
| `reduction_unit` | `JEV_REDUCTION_UNIT` | `characters` | unit `min_reduction` is measured in: `characters`, or `tokens` estimated as below |
| `max_state_tokens` | `JEV_MAX_STATE_TOKENS` | `25000` | token budget of the state, below `max_request_tokens` |
| `max_request_tokens` | `JEV_MAX_REQUEST_TOKENS` | `30000` | token budget of one request |
| `timeout` | `JEV_TIMEOUT` | `2m0s` | timeout of one request, as a Go duration |
| `retries` | `JEV_RETRIES` | `0` | times a request is sent again after a 429 or a 5xx answer, at most 10, `0` for none |
| `retry_backoff` | `JEV_RETRY_BACKOFF` | `1s` | first wait before a retry, as a Go duration, doubled at each retry and capped at 30s |
| `max_requests` | `JEV_MAX_REQUESTS` | `0` | most requests sent to Jev for one compaction, retries included, `0` for no cap |
| `max_sent_tokens` | `JEV_MAX_SENT_TOKENS` | `0` | most tokens sent to Jev for one compaction, retries included and estimated, `0` for no cap |
| `decision_log` | `JEV_DECISION_LOG` | empty | JSONL file where each decision on a tool call is appended, with a `compaction` id per compaction; created or restricted to mode 0600, moved to `<file>.1` (replacing the previous backup) once it reaches 10 MiB, under a `<file>.lock` file lock shared by every process writing the same path; once it is over twice that size and cannot be rotated, writes stop; empty for none |
| `replay_dir` | `JEV_REPLAY_DIR` | empty | directory where each compaction writes one JSON snapshot for `Replay` and `cmd/jev-bench`, created with mode 0700 when missing and its files with mode 0600; an existing directory that gives group or others any access is refused with a warning, as is a snapshot over 64 MiB; a snapshot holds the full history in clear, tool outputs included; a snapshot that cannot be written is logged and the compaction goes on; empty for none |
| `incremental` | `JEV_INCREMENTAL` | `false` | `true` does not ask Jev again about the calls it already said to keep in an earlier compaction of the same session, while their content and the goal stay the same; the cache holds 4096 calls and is emptied when a new call arrives while it is full |
| `max_parallel` | `JEV_MAX_PARALLEL` | `4` | requests sent to Jev at once, at least 1 |
| `on_short` | `JEV_ON_SHORT` | `keep` | `keep` leaves a short reduction to the next stage, `done` stops the stack there, `revert` leaves the history as it was when the reduction is short |
| `dry_run` | `JEV_DRY_RUN` | `false` | `true` computes what Jev would prune and changes nothing |
| `state_input_runes` | `JEV_STATE_INPUT_RUNES` | `1000,200,60` | three cut levels of tool inputs in the state, longest first: full, short, line |
| `state_text_head` | `JEV_STATE_TEXT_HEAD` | `400` | characters kept at the head of a long text when the state abridges it, `0` for none |
| `state_text_tail` | `JEV_STATE_TEXT_TAIL` | `150` | characters kept at the tail of a long text when the state abridges it, `0` for none |
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

`go test ./...` never reaches the network. The live test, which sends a real
compaction to Jev with the key of `TYPESAFE_API_KEY`, sits behind the `live` build
tag and is run on demand:

```bash
go test -tags live -run TestLiveJev ./...
```

`scripts/check.sh` and CI never set the tag.

## License

MIT.
