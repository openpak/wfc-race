# Deploy: wfc-race

Use example.env with the existing WFC database and shared WFC_TOKEN_KEY_FILE.
Never rotate token material or change profile IDs during ordinary service releases.
Dependencies: the leaderboards core (`WFC_LEADERBOARDS_ADDR`, `WFC_LEADERBOARDS_KEY`) and the account core
(`WFC_CORE_ADDRESS`, `WFC_CORE_KEY`) for rankings; see [wfc-common docs/leaderboards.md](https://github.com/openpak/wfc-common/blob/main/docs/leaderboards.md).

```sh
GOWORK=off go test -mod=vendor ./...
GOWORK=off go vet -mod=vendor ./...
docker build -t wfc-race .
```

Shared wire types, errors, API clients and legacy database helpers are vendored
from wfc-common. The image and tests build from this repository alone. Refresh
vendor with the sibling library available after shared contract changes.
See [the full split guide](https://github.com/openpak/wfc-common/blob/main/docs/service-split.md) for full topology, failure/recovery behavior and
migration.
