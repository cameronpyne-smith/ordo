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
ordo add "Put the bins out" --due 2026-09-22 --difficulty low
ordo list                        # open tasks, in order
ordo list --overdue --limit 5
ordo set 4 priority=high due=    # an empty value clears a field
ordo done 4 --minutes 25
ordo undo 4
ordo rm 4
ordo status
ordo backup                      # on the box; serve also snapshots nightly
```

Listing order is fixed and explainable from the fields: overdue first, then by
due date with undated last, then priority, then difficulty so quick wins float
within a tier, then oldest first. No ordering an LLM produced is ever stored.

## Deploy

The repo is cloned on the box and built there.

```sh
git pull && go build -o ordo ./cmd/ordo && sudo systemctl restart ordo
```

`deploy/ordo.service` is a starting point; set `User` and `ExecStart` to match
the box.

## Layout

| Package | Holds |
|---|---|
| `internal/store` | SQLite, the task model, validation, ordering, backups |
| `internal/api` | Wire types and conversion |
| `internal/server` | HTTP routes, bearer auth |
| `internal/client` | HTTP client used by every CLI command |
| `internal/config` | The one config schema |
| `cmd/ordo` | Cobra commands |
