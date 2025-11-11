# WorldClass Scheduler

Command‑line assistant for checking and booking group classes on https://members.worldclass.ro. The tool logs in with your credentials, scrapes the club schedules, filters them according to your interests, and can either list the matching sessions or automatically attempt to book them (including a continuous “loop” mode that wakes up as soon as the booking window opens).

## Features

- **Fetch shortlists**: `fetch` prints all matching classes (or everything with `--all`) along with their current booking state.
- **Automated booking**: `schedule` attempts to reserve every matching class immediately; add `--loop` to keep the process running indefinitely, waking up 26h 1m before each class.
- **Config driven**: Credentials, clubs, interests, timezone, and Sentry DSN all live in `config.yaml`.
- **Observability**: Loop mode reports failures (and successes) to Sentry when a `dsn` is provided.

## Requirements

- Go 1.25 or newer (module declared in `go.mod`).
- WorldClass member credentials.

## Configuration

1. Copy the example file and edit it:

   ```bash
   cp config.yaml.example config.yaml
   $EDITOR config.yaml
   ```

2. Key sections:

   - `base_url`: Leave as `https://members.worldclass.ro` unless the portal changes.
   - `timezone`: IANA identifier (e.g., `Europe/Bucharest`). Used to calculate booking alarms.
   - `credentials`: `email` and `password` for your account.
   - `sentry.dsn` (optional): Fill in to enable Sentry alerts in loop mode.
   - `clubs`: List of `{id, name}` pairs to poll.
   - `interests`: Map of club names to interested classes. Each entry needs:
     - `day`: Day label as shown on the site (Romanian), used for scraping.
     - `day_english`: English weekday name (e.g., `Monday`), used for scheduling math.
     - `time`: Start/end string exactly as it appears online (only the start time is parsed).
     - `title`: Substring (case-insensitive) that should appear in the class title. Leave empty to match any.

## Running

Use `go run` to execute directly:

```bash
go run ./cmd/worldclass-scheduler --config config.yaml fetch
go run ./cmd/worldclass-scheduler --config config.yaml fetch --all
go run ./cmd/worldclass-scheduler --config config.yaml schedule
go run ./cmd/worldclass-scheduler --config config.yaml schedule --loop
```

Notes:

- `--config` defaults to `config.yaml` in the current directory (also overridable via `WORLDCLASS_CONFIG`).
- `fetch` understands `--all` to bypass interest filtering.
- `schedule` accepts `--loop` to keep the process alive and booking future classes automatically.

## Building

Standard build:

```bash
go build ./cmd/worldclass-scheduler
```

Static Linux binary for x86_64:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -a -installsuffix cgo ./cmd/worldclass-scheduler
```

The resulting binary reads `config.yaml` at runtime, so keep the configuration file alongside the executable (or pass `--config /path/to/file`).

## CLI Overview

```
worldclass-scheduler --config <file> [command] [flags]

Commands:
  fetch     Fetch classes and print their status
    --all   Show every class, ignoring interests

  schedule  Attempt to book interested classes
    --loop  Run continuously, waking up for each future class
```

Both commands honor `--config` (or `WORLDCLASS_CONFIG`) to point at a specific configuration file.
