# Mario Kart Wii read API (for OpenKart)

Served by wfc-race on its internal HTTP listener (`WFC_HTTP_ADDR`, default `:20208`), container
network only, never published. Every request needs the header `X-WFC-Internal-Key` with the value
of `WFC_INTERNAL_KEY`. The site that consumes it is [openpak/mktv](https://github.com/openpak/mktv).
Same shape as the Mario Kart 8 read API: lists are `{total, offset, limit, items}`, `limit` defaults
to 20 and is capped at 100, profile ids are decimal strings, errors are `{"error": "..."}` with 400,
404 or 503 (store down; no detail leaked). Base URL from the site's container:
`http://openpak-wfc-race:20208/api/mkw/v1`.

| Endpoint | Returns |
| --- | --- |
| `GET /courses` | `{items:[{course, name, ranked, ghosts}]}` for courses 0-31 |
| `GET /courses/{n}/rankings?region=&limit=&offset=` | full ranking, fastest first: `{rank, player, time_ms, time, updated, name, region, ghost}`; `region` 0-7, default 0 worldwide; `name` is the Mii name; `ghost` is the object id of the stored ghost with that exact time when one exists. `n` may also be a competition id (up to 32767) |
| `GET /courses/{n}/ghosts?owner=&limit=&offset=` | archived ghosts of course 0-31, fastest first, with metadata (below) |
| `GET /objects/{id}` | one archived ghost's metadata |
| `GET /objects/{id}/body` | the stored `.rkg` file, byte for byte, as an attachment (`mkw-<course>-<time>-<id>.rkg`) |
| `GET /players/{pid}/rankings` | worldwide `{player, course, rank, of, time_ms, time}` per course for one GameSpy profile id |
| `GET /users/{userid}/rankings` | the same for every profile id of one console user id: `{user_id, players:[pid...], items}` |
| `GET /tournaments`, `GET /tournaments/{id}` | competitions (see below) |

The legacy `GET /api/mkw/leaderboard?region=&course=` (top ten) is unchanged.

## Ghost object

MK8-shaped (`id, owner, name, size, data_type, slot, tags, meta_size, thumbnail, rating, created,
updated`) with `data_type` = the course, `name` = the Mii name, `tags` = vehicle, character and
controller names, plus `course, course_name, time_ms, time, region, vehicle(_name), character(_name),
controller(_name), date, laps_ms, drift_type, ghost_type, lap_count, country_code, state_code,
location_code`. `date` is the day the run was set, read from the RKG header (empty if the header
holds an impossible date). The object id is the archive row id, not the game's `fileid`.

## Course ids (FACT, wfc-common `MarioKartWiiCourseId`)

0-31 in the game's order: 0 Mario Circuit ... 7 Wario's Gold Mine, 8 Luigi Circuit ... 15 Koopa
Cape, 16 GCN Peach Beach ... 23 DS Delfino Square, 24 SNES Mario Circuit 3 ... 31 GBA Shy Guy Beach
(`GET /courses` lists the names). Ids 32-32767 are accepted by the game's ranking service and by
uploads with the `contest` flag; the server treats them as competitions.

## Looking results up from a linked console

The core account holds a link in namespace `wfc` whose subject is the console-level `user_id`
(uint64) of the `users` table. wfc-sake rows are keyed by `pid`, the GameSpy profile id (`users.profile_id`),
and one console has one row per game it registered. `GET /users/{userid}/rankings` resolves
`users.user_id` to all of its profile ids and returns the standings of each; a console that never
played gets empty lists (200), not 404.

## Competitions (`/tournaments`)

INFERRED from the existing server code: a ranking or upload with a course id above 31 and the
`contest` flag is a Mario Kart Channel competition; its entries are in the same table, without a
ghost body. `GET /tournaments` lists competition ids that have entries (newest first);
`GET /tournaments/{id}` returns the id with the top 100. The game has no seasons for these, so
`?season=` is accepted and ignored. No competition has been seen on this server yet.

## Where the data lives

`mario_kart_wii_sake` (one best row per course and profile, what the game ranks) and
`mario_kart_wii_ghosts` (every accepted upload with its RKG header metadata; see
[wfc-common](https://github.com/openpak/wfc-common/blob/main/docs/mario-kart-wii-ghosts.md)). The schema is created
and the existing ghosts are backfilled at start by wfc-race and wfc-sake.

Sources for the RKG header: Wiimm's RKG documentation, wiki.tockdom.com "RKG", WiiLink wfc-server docs (facts only).
