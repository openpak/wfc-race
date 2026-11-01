# Protocol: wfc-race

Internal HTTP defaults to WFC_HTTP_ADDR=:20208. GET /healthz is unauthenticated;
protocol/internal paths require WFC_INTERNAL_KEY. Console HTTP reaches this owner
through wfc-nas; TCP/UDP ports belong directly to their protocol's container.
Runtime frontend/backend net/rpc endpoints stay on loopback inside each TCP
service container. Restarting another protocol does not restart these sockets.

The SOAP action `GetTopTenRankings` (Mario Kart Wii, game id 1687) is the only one handled; `SubmitScores` is not (scores arrive through wfc-sake's ghost upload). The JSON read API is in [read-api.md](read-api.md).
