# Changelog

## v0.5.1 - 2026-11-05

- The leaderboards adapter reads the account from the local link cache first (link-first subjects, prds#139). Race serves only anonymous top-ten reads, so there is nothing to gate. Vendors wfc-common v0.8.0.

## v0.5.0 - 2026-11-04

- Rankings now come from the neutral leaderboards core (wfc-common v0.4.0 `mkwboard`): the SOAP `GetTopTenRankings`, the JSON top ten and the `/api/mkw/v1` read API (OpenKart) read the core; the JSON shapes are unchanged. Ghost files and the ghost archive stay in the WFC database. Needs `WFC_LEADERBOARDS_ADDR`/`_KEY` and `WFC_CORE_ADDRESS`/`_KEY`; unset falls back to the game's own table.
- The rankings-contract copy (`RANKINGS_ADDR`, `/internal/rankings/*`) is removed: the website reads `http://openpak-leaderboards:20210/t/mario-kart-wii`.
- Only consoles linked to an account are ranked (the core ranks accounts).

## v0.4.3 - 2026-11-03

- Internal `/internal/` polls are counted and summarised once a minute instead of logged per call (wfc-common v0.3.1).

## v0.4.2 - 2026-11-03

- Every HTTP request now logs one line to stderr, `http <method> <path> -> <status> <ms>ms` (path only, never the query, headers or bodies; `/healthz` skipped), through wfc-common v0.3.0 `httplog`.

## v0.4.1

- Rankings contract: rows carry `pid` = the console id (`users.user_id`), the subject the account core links
  a Wii under, so the website can name the player; a profile whose console is gone has no `pid`. The
  `around` parameter takes that console id. Boards are the 32 worldwide courses only (the per-region
  tables stay on `/api/mkw/v1`). Requires wfc-common v0.2.2.

## v0.4.0

- Rankings contract (`/internal/rankings/boards`, `/boards/{id}`) over the Mario Kart Wii time-trial
  table, on its own listener when `RANKINGS_ADDR` is set (empty = off). 32 worldwide course boards,
  then each course per region; rows carry the GameSpy profile id as `pid`, network `wfc`, with an
  `around` slice. Requires wfc-common v0.2.1 (`MKWRankOf`).
- `/api/mkw/leaderboard` and the read API are unchanged.

## v0.3.0

- Read API under `/api/mkw/v1/` for the OpenKart site: courses, full rankings with paging and
  region filter, archived ghosts with RKG header metadata, ghost downloads, per-player and
  per-console-user rankings, competitions. See [docs/read-api.md](docs/read-api.md).
- Requires wfc-common v0.2.0 (ghost archive table, created at start).
- `/api/mkw/leaderboard` and the SOAP service are unchanged.

## v0.2.0

Previous release.
