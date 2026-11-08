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
to edit its history, and the herrscher host release that fills
`CompactRequest.History` (the next one). Without both, the stage does nothing,
returns its `no_history` note, and lets the backend compact.

The status stays live: the stage and the host release that feeds it ship
together, and until then the stage steps aside on its own.

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
5. Decide per call, against `keep_threshold`:
   - the output is still needed: keep the call untouched;
   - only the call is: keep its first `truncate_head` characters of output
     (`PruneResult`), when the output is longer than that by more than 120;
   - neither: prune the call (`PruneCall`), which keeps the call in place with
     its input shortened and its output replaced by a note.
6. Apply every edit in one `PruneHistory` call.
7. Report `Done` when the history shrank by at least `min_reduction`, so the
   backend's own compaction runs only when pruning was not enough. The share
   is measured against the whole history, message text included, not only
   against the tool inputs and outputs the pruning touches.

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

## Settings

| Key | Env | Default | Meaning |
|---|---|---|---|
| `api_key` | `TYPESAFE_API_KEY` | required | TypeSafe API key |
| `model` | `JEV_MODEL` | `jev-latest` | Jev model asked |
| `endpoint` | `JEV_ENDPOINT` | `https://api.typesafe.ai/v1/systemone` | System One URL, https only |
| `keep_threshold` | `JEV_KEEP_THRESHOLD` | `0.5` | probability, 0 to 1, at or above which a call or its output stays |
| `preserve_recent` | `JEV_PRESERVE_RECENT` | `6` | newest messages whose calls are pinned |
| `truncate_head` | `JEV_TRUNCATE_HEAD` | `300` | characters kept from an output Jev lets go |
| `min_reduction` | `JEV_MIN_REDUCTION` | `0.25` | share of the history, 0 to 1, that stops the stack |
| `max_state_tokens` | `JEV_MAX_STATE_TOKENS` | `25000` | token budget of the state |
| `max_request_tokens` | `JEV_MAX_REQUEST_TOKENS` | `30000` | token budget of one request |
| `timeout` | `JEV_TIMEOUT` | `2m0s` | timeout of one request, as a Go duration |

Tokens are estimated without a tokenizer, with the weights fast-jev calibrated
against what Jev reports: a word costs one token per six letters, a digit half
a token, any other symbol nine tenths. Symbols are counted in UTF-16 units
and spaces are the JavaScript `\s` set, as in fast-jev, so a character
outside the Basic Multilingual Plane costs two symbols.

## Develop

```bash
scripts/check.sh
```

runs the quality chain of the go-book: tidy, format, golangci-lint, the
comment check, tests with `-race`, build and govulncheck.

## License

MIT.
