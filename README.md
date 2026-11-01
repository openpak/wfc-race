# Wii/DS Race Server

OpenPak's Wii/DS Race Server.

| | |
| --- | --- |
| Network | Wii, DS |
| Role | Nintendo race and leaderboard SOAP protocol |
| Depends on | WFC database and shared `WFC_TOKEN_KEY_FILE`; no remote WFC service |
| Image | `ghcr.io/openpak/wfc-race` |
| Based on | [WiiLink WFC](https://github.com/WiiLink24/wfc-server) (AGPL-3.0) |
| Licence | AGPL-3.0-only |

## What it serves

| Endpoint | What |
| --- | --- |
| `GET /healthz` | Health check, unauthenticated |
| `GET /api/mkw/leaderboard?region=&course=` | JSON top ten of one Mario Kart Wii course (`region` 0-7, default 0 worldwide; `course` 0-31): `{region, course, entries:[{rank, pid, time_ms, name}]}`; read by the website; needs `WFC_INTERNAL_KEY` |
| `GET /api/mkw/v1/...` | Read API for the OpenKart site: courses, rankings, ghosts, ghost downloads, player and console-user rankings, competitions; needs `WFC_INTERNAL_KEY`; see [read API](docs/read-api.md) |
| Protocol and internal paths | Require `WFC_INTERNAL_KEY` |
| Console HTTP | Reaches this service through `wfc-nas` |

## Run

```sh
GOWORK=off go test -mod=vendor ./...
GOWORK=off go vet -mod=vendor ./...
docker build -t wfc-race .
```

## Docs

- [Read API](docs/read-api.md)
- [Changelog](CHANGELOG.md)
- [Protocol](docs/protocol.md)
- [Evidence](docs/evidence.md)
- [Deploy](docs/deploy.md)
- [Notes](docs/notes.md)

## Licence

AGPL-3.0-only. See [LICENSE](LICENSE).
