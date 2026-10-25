# wfc-race

Independent OpenPak Wii/DS protocol service: Nintendo race and leaderboard SOAP protocol.
Own repository, binary, image and release; AGPL-3.0, derived from WiiLink WFC.

Internal HTTP defaults to WFC_HTTP_ADDR=:20208. GET /healthz is unauthenticated;
protocol/internal paths require WFC_INTERNAL_KEY. Console HTTP reaches this owner
through wfc-nas; TCP/UDP ports belong directly to their protocol's container.
Runtime frontend/backend net/rpc endpoints stay on loopback inside each TCP
service container. Restarting another protocol does not restart these sockets.

Use example.env with the existing WFC database and shared WFC_TOKEN_KEY_FILE.
Never rotate token material or change profile IDs during ordinary service releases.
Dependencies: no remote WFC service dependency.

```sh
GOWORK=off go test -mod=vendor ./...
GOWORK=off go vet -mod=vendor ./...
docker build -t wfc-race .
```

Shared wire types, errors, API clients and legacy database helpers are vendored
from wfc-common. The image and tests build from this repository alone. Refresh
vendor with the sibling library available after shared contract changes.
See nn-wfc docs/service-split.md for full topology, failure/recovery behavior and
migration. Protocol tests do not claim real-console gameplay validation.
