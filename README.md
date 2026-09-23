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

[calendar]
ics_url         = "https://calendar.google.com/calendar/ical/.../private-xxxx/basic.ics"
publish_to      = "xxxx@group.calendar.google.com"
service_account = "/home/you/.config/ordo/google.json"
```

Laptop:

```toml
server = "http://100.103.58.27:7930"
token  = "..."
```

`server` falls back to `bind`, so a machine running both needs no extra lines.

## Commands

```sh
ordo                             # the terminal view
ordo add "Put the bins out" --every "weekly on tue" --difficulty low
ordo add "Water the plants" --after 3d
ordo add "Send the CV" --due 25/09/26 --priority high   # or 2026-09-25
ordo add "Re-rate the skills matrix" --start 15/10/26 --waits-on 10,11
ordo list                        # open tasks, in order
ordo list --overdue --limit 5
ordo list --recurring
ordo list --note career-transition-quantitative-researcher
ordo set 4 priority=high due=    # an empty value clears a field
ordo set 12 waits_on=10,11       # the whole list; waits_on= clears it
ordo set 12 start=15/10/26       # not in the plan before then
ordo done 4 --minutes 25
ordo work 4 60                   # an hour on it, not finished
ordo work 4 60 --left 180        # ...and it turned out bigger than that
ordo set 4 left=240              # re-estimate what is left without logging work
ordo undo 4                      # the last completion or session
ordo enrich 4                    # read it with the local model again
ordo link 4 latent               # point a task at a mnemo note
ordo link 4                      # ...or see which notes it could point at
ordo related 4
ordo unlink 4
ordo rm 4
ordo today                       # the day, planned from its free time
ordo today --why                 # and why each block is where it is
ordo pin 4                       # claim a task for today
ordo unpin 4
ordo prefs                       # the shape of the day
ordo prefs deep_start=06:00 day_start=06:00
ordo status
ordo backup                      # on the box; serve also snapshots nightly
```

Listing order is fixed and explainable from the fields: overdue first, then by
deadline with undated last, then priority, then difficulty so easy work floats
within a tier, then oldest first. No ordering an LLM produced is ever stored.

## Waiting

**A start date** holds a one-off task out of the plan until that day; the due
date stays the deadline. A repeating task has none, since its next date
already says when it comes up. The model sets one only from an explicit
"from monday" or "not before the 15th".

**A dependency** says a task cannot start until others are finished. A task
can wait on several and hold up several. Only open one-off tasks can be
waited on, since a repeating one is never finished, and a loop is refused
with the chain it would close. Finishing the last blocker releases the task,
undoing that holds it up again, and deleting a blocker releases whatever it
was holding. A task can still be marked done while it waits, because what
happened wins. Dependencies are never inferred: a wrong guess would hide a
task.

A waiting task is left out of the plan and out of quick wins, unless it is
pinned: a pin is "today, regardless". **Its deadline passes down** to what it
waits on: a blocker has to be done by the dependent's deadline less the days
the dependent needs at one sitting a day, down a whole chain, and a
blocker's own earlier date still wins. So 12, due 15 Oct with 90 minutes
left, makes 10 and 11 due 13 Oct, and the list and the plan both order them
by that. The list shows the date with whose it is:

```
$ ordo list
ID  DUE                PRIORITY  DIFFICULTY  TITLE
10  2026-10-13 for 12  high      high        Read AFML chapter 11
12  2026-10-15         normal    high        Re-rate the skills matrix (waits on 10, 11)
```

## Recurring tasks

A task repeats either on a schedule or an interval after it was last done.

```
--every  daily | "weekly on tue" | "weekly on mon,thu" | "monthly on 1" |
         "monthly on last" | "yearly on 03-15" | 3d | 2w | 1m
--after  3d | 2w | 1m
```

Both take an interval, and the kind is what separates them. `--every 2w` is a
fortnightly cycle: it counts from the occurrence, so descaling the machine two
days early still lands a fortnight after the date it was due. `--after 2w`
counts from the day it was actually done, so the same two days early move
every later one earlier too. Fixed cycles want `every`; "leave it a fortnight
and do it again" wants `after`.

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

## The terminal

Bare `ordo` opens the list in the terminal. It is a thin client like every
other command: the daemon decides the order, and the terminal only groups it
into **overdue**, **today**, **this week**, **later**, **someday** and
**waiting**, which holds what cannot be started yet, each row saying why:
"waits on 10, 11" or "from 2026-10-15". A section can never reorder
anything, because a task falls into the first one it qualifies for and the
daemon's order is kept inside it.

```
ordo · open                                                            6 shown
────────────────────────────────────────────────────────────────────────────
overdue
▸  11 2026-09-19 ! Send the quant CV to the recrui [[career-transition-quant…
today
   10 2026-09-21 Water the plants every 3 days
this week
    9 2026-09-22 Put the bins out every tuesday
someday
   14            Rewrite the latent pricing model [[latent]] (missing)
   16            Renew the passport ~
────────────────────────────────────────────────────────────────────────────
Send the quant CV to the recruiter by Friday
overdue by 2 days · high priority · low difficulty · a quick win
[[career-transition-quantitative-researcher]] The quant plan
────────────────────────────────────────────────────────────────────────────
1 open  2 overdue  3 quick wins  4 high priority  5 linked  6 recurring  7 done
/ search · enter open · a add · d done · w worked · u undo · e enrich · l note · p pin · t today · x delete · q quit
```

The line under the list is the point of the thing: it names the fields that
put the selected task where it is, so a position never has to be taken on
trust. `~` means the model has not read the task yet.

The number keys are the filters, and they are filters rather than a
conversation on purpose — "I have twenty minutes" is `3`, not a question.
`/` searches within whichever filter is on, narrowing the list as you type:
every word has to appear somewhere in the title, the notes or the slug of
the linked note, and a number also finds the ids that start with it. Enter
keeps the search so the match can be opened or done, and esc clears it.
`a` adds a task: type the sentence and the daemon extracts the rest of it,
so the new row appears bare and fills in a second or two later while you
watch. `d` asks how long the task took before completing it, with the
estimate already filled in: enter keeps it, a number replaces it, an empty
line records no time, and esc completes nothing. Those minutes are what the
estimates will be calibrated against. `w` is for work that is not finished:
it asks the same question, then how much is left, filled in with what was
left less the time just spent. Enter keeps it; a number says the job turned
out bigger or smaller than that, since time spent is not progress; 0 means
it is finished after all. `d` always finishes and `w` never does, and `u`
takes back whichever was last. A repeating task is done in one go, so `w`
refuses one. A quick win is anything with half an hour or less to go,
whatever its difficulty, including the last stretch of a big task; with no
estimate at all, only low difficulty counts. `l` opens what mnemo knows about the selected task — the note and its
neighbourhood if it is linked, the notes it could point at if it is not, or
the notes it might have become if its own has been renamed away.

`enter` opens the selected task. Priority and difficulty are enums, so `p`
and `d` cycle them and shift steps back; every other field opens a line
prefilled with what is already there, because a due date is nearly always a
correction rather than a retype. Each press saves, and the pane redraws from
what the daemon stored rather than from what was typed.

A title too long for its row in the list is wrapped whole at the top of the
open task, and editing it scrolls the line sideways rather than off the screen.

Notes are the exception, having no natural length. A note too long for its
row is shown whole under the fields, wrapped, with `j` and `k` to scroll it,
and `n` opens it in an editor of several lines: enter starts a new line,
ctrl+s saves and esc discards.

```
ordo · #3
────────────────────────────────────────────────────────────────────────────

  Rewrite the latent pricing model

  u  due         2026-10-01
  s  start       —
  p  priority    high
  d  difficulty  high
  m  estimate    90 min
  r  repeats     3d after each completion
  n  notes       the forward curve is the part that is wrong
  t  title       Rewrite the latent pricing model
  w  waits on    13 Download the market data
                 11 Fix the calendar ✓
     blocks      15 Publish the pricing note

  [[latent]] Full reference dump for the project
────────────────────────────────────────────────────────────────────────────
p d cycle · shift reverses · u s m l r n t w edit · esc back · ? help · q quit
```

`w` opens a picker over the open tasks: type to filter the way `/` searches, space ticks, enter saves the whole list and esc changes nothing.
It only offers what the task could wait on, so nothing repeating and nothing
that already waits on this one. A blocker already done is ticked with ✓ and
can be kept or dropped. `blocks` is what waits on this task.

Once work has started the pane gains a `left` row under the estimate, and
`l` edits it: that is a re-estimate part way through, and it logs no work.
The estimate stays the first guess from then on, so a finished task can be
set against what it really took; left is what the plan uses, and clearing it
goes back to the estimate.

Cycling never lands on the unset value: clearing a field is deliberate
enough to be worth typing, and one press too many should not throw away what
the model worked out. An empty line is how a field is cleared. Whatever is
set here is final, since enrichment only ever fills a field that is still
empty.

While anything on screen is still unread the list refreshes every 2 seconds;
once everything has been read it drops to 30. A task open in the pane keeps
up with it, so a field the model fills in appears without reopening it.

## The day

`ordo today` plans one day. It takes the hours the day is available, cuts out
work and whatever the calendar says is taken, and fills what is left from the
list in the daemon's own order. A plan for today starts from now, rounded up
to the next five minutes: asked at three in the afternoon, it does not put
anything at seven in the morning.

```
$ ordo today --why
2026-09-21  90 of 180 minutes planned

07:00-07:30     3  Send the quant CV        30 min
08:00-08:45     1  Wooldridge chapter 2     45 min
12:30-13:00        Call with the recruiter  (calendar)
17:30-17:45     2  Water the plants         15 min
19:00-21:00        Climbing                 (calendar)

07:00  overdue by 1 day · high priority · 30 min guessed from its difficulty
08:00  nothing forced it, so the next one on the list · demanding, so it takes the deep-work window
17:30  due today · 15 min guessed from its difficulty

not today
  4    Renew the passport — needs 200 minutes and only 90 of the day's 180 are left
```

**It is deterministic and nothing stores it.** The same tasks, calendar and
preferences always give the same day, no language model is anywhere near it,
and the plan is computed fresh on every read rather than written down. A plan
you cannot predict is one you end up arguing with, and a stored one is a
decision someone made earlier pretending to be the current answer.

Every block says why it is there, and so does every task that did not make
it: `--why` is the same promise the list makes about its order. An estimate
the model has not measured is named as a guess rather than quietly used.

Two things decide placement beyond the daemon's order. A **pin** claims a
task for a day ahead of whatever the order would have chosen, and names a
date, so it never carries into tomorrow by itself. **Demanding work takes the
deep-work window** first, and a task too long to fit inside it claims nothing
and is placed normally, because preferring a window is not the same as
requiring one.

**A task too big for one sitting is worked on a piece a day.** Any one-off
task with more left than `max_block_minutes` gets that much each day from the
day it is added, not from its due date, in its usual place in the order.
When the days before the deadline are too few for that, the piece grows to
what is left divided by the days to go, today included, and the reason says
it is catching up. With no gap long enough, a piece shrinks into the longest
one there is, down to half an hour; a task meant to be done in one go never
shrinks. A task gets one piece a day at most, a repeating task is never cut
up, and logging a session with `w` or `ordo work` is what moves the rest
along: tomorrow's piece comes out of what is left.

```
07:00-08:00     4  Write the report         1h of 5h left
```

```sh
ordo prefs
day_start            07:00     # the earliest anything is scheduled
day_end              22:00
deep_start           07:00     # demanding tasks prefer this window
deep_end             09:00
buffer_minutes       10        # left between consecutive blocks
min_block_minutes    15        # shorter stretches are not offered
max_minutes_per_day  240       # the cap on what one day is given
max_block_minutes    60        # one sitting; bigger tasks go a piece a day
```

Changing one leaves the rest alone, and a day that could not exist is refused
whole rather than half-written: deep work outside the usable day is a clash,
not a silent no-op.

Working hours are not a preference. Work goes on the calendar as events like
anything else that takes the day, because a job has lunch breaks, leave, half
days and late meetings that two clock times cannot say.

## The calendar

The calendar has two directions, and they are two different calendars.

**Reading.** `[calendar] ics_url` is a published ICS feed of your own
calendar, the one with your commitments in it: work, meals, the school run,
anything ordo should plan around. The scheduler reads it for what is already
taken and never writes to it. Only events marked busy count.

A feed is optional; without one, every hour between `day_start` and
`day_end` is free. Once one is configured it is what keeps tasks out of the
working day, so an unreadable feed is answered from the last copy that could
be read, and the plan says how old that copy is. A daemon that has never
read the feed since it started does not publish until it has: leaving the
last published plan in place is better than filling the working day with
tasks on every phone the calendar reaches.

Recurring events, exclusions, all-day events, durations instead of end times,
and events marked free or cancelled are all handled: most of what fills a
calendar repeats, so a reader that ignored `RRULE` would miss the majority of
a real week. One unparseable event is skipped rather than losing the rest.

Any provider with a private ICS address works. Point it at a personal
calendar rather than a work one, and put work on it as a recurring busy
event: a corporate tenant usually blocks publishing anyway, and ordo only
needs to know when you are busy, not what the meetings are.

**Publishing.** `publish_to` is a Google calendar that exists for ordo alone.
The daemon keeps it equal to the plan: one event per block, the task as the
title and the reason as the description, so a block on your phone still says
why it is there. It is reconciled rather than appended to — every pass
recomputes the day, lists what the calendar has, and inserts, patches or
removes until the two match — so a block that moves, moves, and a task you
finish disappears. A pass runs at startup, two seconds after anything the
plan is built from changes, and every five minutes regardless, because a plan
for today moves with the clock even when the list does not.

ordo only touches events it made, which it marks with a private property no
calendar app shows. Anything you add to that calendar by hand is left alone.
Blocks are transparent, so they never make you look busy to anyone who can
see the calendar, and reminders follow the calendar's own defaults.

The two calendars must be different. If ordo published into the calendar it
reads, it would see its own blocks as busy time and plan around them.

Publishing needs a service account: a robot identity with a JSON key, which
suits a daemon far better than a browser sign-in whose tokens expire. Once:

1. At console.cloud.google.com make a project, and under **APIs & Services →
   Library** enable the **Google Calendar API**.
2. Under **IAM & Admin → Service Accounts** create one (no roles are needed),
   open it, and under **Keys → Add key → JSON** download its key. Put the file
   on the box, readable only by the daemon's user, and set `service_account`
   to its path.
3. In Google Calendar create a calendar called `ordo`. In its settings, under
   **Share with specific people or groups**, add the service account's email
   — the `...@...iam.gserviceaccount.com` address from the key — with **Make
   changes to events**.
4. Further down the same page, under **Integrate calendar**, copy the
   **Calendar ID** into `publish_to`.

Restart, and the log says `publishing the day to a calendar` and then what
the first pass did. `publish_days` widens the horizon past today; the default
of one is deliberate, since tomorrow's plan is made as if nothing were
finished today and would show the same tasks twice.

## Claude

The daemon serves MCP at `/mcp` on the same port, behind the same bearer
token, so a Claude session gets the same operations as the CLI: `todo_list`,
`todo_add`, `todo_done`, `todo_work`, `todo_undo`, `todo_set`, `todo_link`,
`todo_related`, `todo_delete`, `todo_today`, `todo_pin`, `todo_preferences`.

```sh
claude mcp add --transport http ordo http://100.103.58.27:7930/mcp \
  --header "Authorization: Bearer <the token from your ordo config>"
```

The tool schemas carry the enums the daemon enforces, so a wrong difficulty
or priority is refused before the call is made rather than coming back as a
400.

**This is the point of the phase.** With ordo and mnemo in the same session,
an action item becomes a task the moment it appears in the conversation,
created by something that understood the sentence — instead of being written
into a note and having to be found again later. mnemo goes back to being
knowledge; ordo holds what is to be done.

A paragraph worth having in `CLAUDE.md` alongside the mnemo one:

```markdown
## ordo — personal todo daemon
The `todo_*` MCP tools are my task list. mnemo remembers, ordo orders.

**Anything I say I need to do is a `todo_add`, never a note.** Title it
with my whole sentence, unedited: a model on the box re-reads that
sentence and fills in due date, difficulty, recurrence and priority, so a
title you shortened is information it no longer has.

**Set a field when you know something the sentence does not.** I named a
deadline earlier in the conversation; you have read the code and know it
is a day's work. Context rather than a decision goes in `notes`, which
that model reads too. Do not restate what the sentence already says and do
not guess: inference only fills fields that are still empty, so whatever
you set is final.

**Priority is yours.** It is about my week, the one thing the box cannot
see. Reprioritising, planning a week and "what should I focus on" mean
`todo_list` and then `todo_set`.

**Read the list rather than recalling it.** `todo_list` before answering
anything about what is next; fields appear a second or two after the add.

**`todo_link` a task to the note it came out of.** The note is the
thinking, the tasks are what is in flight from it. `todo_related` finds
the slug when you do not know it. ordo only reads mnemo; nothing here
changes a note.
```

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

Every request the daemon answers is logged with its method, path, status,
duration and the address it came from, including the ones refused for a bad
token — a client being turned away silently is the hardest thing to diagnose
from the other end.

```
level=INFO msg=request method=POST path=/tasks status=201 ms=3 from=100.103.58.27
level=INFO msg=request method=GET path=/today status=200 ms=11 from=100.64.0.4
```

An update that carries a migration copies the database beside itself first,
as `ordo-pre-v4-20260922-140000.db`, and says where in the log. The nightly
snapshot can be most of a day old by the time a new version is deployed, and
a migration is the one moment code that has never run against this data
rewrites it.

## Layout

| Package | Holds |
|---|---|
| `internal/store` | SQLite, the task model, validation, ordering, backups |
| `internal/recur` | The recurrence grammar and next-due arithmetic |
| `internal/ollama` | The client for the box's local model |
| `internal/enrich` | The background worker that reads new tasks |
| `internal/mnemo` | The read-only vault client |
| `internal/calendar` | The read-only ICS feed reader |
| `internal/schedule` | Free windows and the deterministic day packer |
| `internal/gcal` | The Google Calendar client, as a service account |
| `internal/publish` | Keeps the published calendar equal to the plan |
| `internal/api` | Wire types and conversion |
| `internal/todo` | What ordo does, independent of how it is asked |
| `internal/server` | HTTP routes, bearer auth |
| `internal/mcp` | The MCP tools, mounted at `/mcp` |
| `internal/tui` | The terminal view |
| `internal/client` | HTTP client used by every CLI command |
| `internal/config` | The one config schema |
| `cmd/ordo` | Cobra commands |
