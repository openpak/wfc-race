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
| Protocol and internal paths | Require `WFC_INTERNAL_KEY` |
| Console HTTP | Reaches this service through `wfc-nas` |

## Run

```sh
GOWORK=off go test -mod=vendor ./...
GOWORK=off go vet -mod=vendor ./...
docker build -t wfc-race .
```

## Docs

- [Protocol](docs/protocol.md)
- [Evidence](docs/evidence.md)
- [Deploy](docs/deploy.md)
- [Notes](docs/notes.md)

## Licence

AGPL-3.0-only. See [LICENSE](LICENSE).
