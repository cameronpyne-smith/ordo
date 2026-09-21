# ordo

A personal todo daemon. It owns its tasks in SQLite and, where a task is about
something durable, links to a note in [mnemo](https://github.com/cameronpyne-smith/mnemo).
mnemo is read-only to ordo: nothing here ever writes to the vault.

One binary. `ordo serve` is the daemon; every other command is an HTTP client
of it.

## Install

```sh
git clone git@github.com:cameronpyne-smith/ordo.git
cd ordo
go build -o ordo ./cmd/ordo
./ordo init
```

`init` writes `config.toml` under the user config directory, generating a
bearer token if there is none.

## Config

One schema on every machine, at `$XDG_CONFIG_HOME/ordo/config.toml` (or
`%AppData%\ordo\config.toml`). The box running the daemon fills in the daemon
keys; a laptop fills in `server` and `token` and leaves the rest alone.

Box:

```toml
bind       = "0.0.0.0:7930"
token      = "..."
db         = "/var/lib/ordo/ordo.db"
backup_dir = "/mnt/backup/ordo"

[mnemo]
url   = "http://127.0.0.1:7920"
token = "..."

[ollama]
url   = "http://127.0.0.1:11434"
model = "qwen3.6:35b"
```

Laptop:

```toml
server = "http://100.103.58.27:7930"
token  = "..."
```

`server` falls back to `bind`, so a machine running both needs no extra lines.

## Commands

```sh
ordo add "Put the bins out" --every "weekly on tue" --difficulty low
ordo add "Water the plants" --after 3d
ordo add "Send the CV" --due 2026-09-25 --priority high
ordo list                        # open tasks, in order
ordo list --overdue --limit 5
ordo list --recurring
ordo list --note career-transition-quantitative-researcher
ordo set 4 priority=high due=    # an empty value clears a field
ordo done 4 --minutes 25
ordo undo 4
ordo enrich 4                    # read it with the local model again
ordo link 4 latent               # point a task at a mnemo note
ordo link 4                      # ...or see which notes it could point at
ordo related 4
ordo unlink 4
ordo rm 4
ordo status
ordo backup                      # on the box; serve also snapshots nightly
```

Listing order is fixed and explainable from the fields: overdue first, then by
due date with undated last, then priority, then difficulty so quick wins float
within a tier, then oldest first. No ordering an LLM produced is ever stored.

## Recurring tasks

A task repeats either on a schedule or an interval after it was last done.

```
--every  daily | "weekly on tue" | "weekly on mon,thu" | "monthly on 1" |
         "monthly on last" | "yearly on 03-15"
--after  3d | 2w | 1m
```

`ordo set 4 every="monthly on last"` changes the schedule, and moves the due
date onto it; `ordo set 4 every=` stops it repeating and leaves the date
alone.

Completing a recurring task does not close it. The row stays open and keeps
its id for life; its due date moves to the next occurrence and the
completion joins its history. An `every` rule advances from the occurrence
just completed, so putting the bins out on Monday evening moves them to next
Tuesday rather than tomorrow, and once a date has gone by, today takes over
so the weeks you missed do not queue up. An `after` rule counts its interval
from when the task was actually done.

## Enrichment

Adding a task should cost one sentence. The daemon reads that sentence with
the box's ollama and fills in what it can work out: difficulty, priority, a
due date, and whether the task repeats.

It happens in the background. `ordo add` writes the row and returns
immediately; the row fills in a second or two later, and a `~` after the
title marks one the model has not reached yet.

**Nothing you set is ever overwritten.** The model only fills fields that are
empty, so `ordo add "Send the CV" --priority high` keeps that priority
whatever the model thinks. To have it reconsider a field, clear the field and
ask again:

```sh
ordo set 4 due=
ordo enrich 4
```

Changing a title re-queues the task, since a new title is a new sentence; the
same rule applies, so it fills what is still empty rather than revising what
is there.

With no `[ollama]` model configured, or with ollama down, every command works
exactly as before and rows simply stay as they were typed. Tasks missed while
it was down are picked up the next time the daemon starts.

## Notes in mnemo

A task links to a note when something remains after the task is finished.
Bins: nothing remains, no link. "Write the quant CV": the note already
exists, link it.

**Nothing is copied.** A link is a pointer: the task lives here, the knowledge
lives in mnemo, and there is no second copy of either to keep in step. ordo
never reads tasks out of notes and never creates them from what it finds — it
cannot write back, so anything it scraped would be a fact the vault kept
getting wrong for ever. Tasks are created here, in a Claude session or at a
prompt, and linked.

```sh
ordo add "Rewrite the pricing model" --link latent
ordo link 4                      # search the vault with the task's own words
ordo link 4 latent               # link it
ordo related 4                   # the note, what it links to, what links back
ordo list --linked
```

ordo never writes to mnemo. The client has no method that could: linking
stores a slug on ordo's own row, and nothing else moves.

Linking records the note's description as well as its slug. That remembered
line is what finds the note again when mnemo renames or merges it, which it
does on its own. A link whose note has gone is not an error — the task stays,
`ordo list --linked` shows it as `(missing)`, and `ordo related` searches for
where the note went:

```
$ ordo related 4
the linked note is no longer in the vault; it may have been renamed or merged

closest notes
  career-transition-quantitative-researcher  Plan for moving into quant research
  latent                                     The company

  ordo link 4 <slug>
```

`--note` names one note and lists what is in flight for it, which is how a
long plan works: the note holds the whole programme, ordo holds the handful of
session-sized tasks pulled out of it.

```sh
$ ordo list --note career-transition-quantitative-researcher
ID  DUE         PRIORITY  DIFFICULTY  REPEATS  TITLE
7   2026-09-22  normal    medium      daily    Green Book: 5 timed problems [[career-...]]
9   2026-09-27  high      high        -        Wooldridge ch. 2, exercises [[career-...]]
```

Only `--linked` and `--note` check the links, since those are the views whose
point they are; a plain `ordo list` stays one database read. With mnemo unreachable
nothing is marked missing — "I could not ask" is not "it is gone" — and every
command except the three link ones works as usual. `ordo unlink` works even
then: cutting a link is ordo's own business.

## Deploy

The repo is cloned on the box and built there.

```sh
git pull && go build -o ordo ./cmd/ordo && ./ordo serve
```

To have it survive a reboot, `deploy/ordo.service` is a starting point.
Replace `YOUR_USER` throughout: the daemon must run as the account whose
config file holds the token, since the daemon and the CLI read the same
`config.toml`.

```sh
sudo cp deploy/ordo.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ordo
journalctl -u ordo -f
```

With the unit installed, an update is `git pull`, `go build`, then
`sudo systemctl restart ordo`.

## Layout

| Package | Holds |
|---|---|
| `internal/store` | SQLite, the task model, validation, ordering, backups |
| `internal/recur` | The recurrence grammar and next-due arithmetic |
| `internal/ollama` | The client for the box's local model |
| `internal/enrich` | The background worker that reads new tasks |
| `internal/mnemo` | The read-only vault client |
| `internal/api` | Wire types and conversion |
| `internal/server` | HTTP routes, bearer auth |
| `internal/client` | HTTP client used by every CLI command |
| `internal/config` | The one config schema |
| `cmd/ordo` | Cobra commands |
