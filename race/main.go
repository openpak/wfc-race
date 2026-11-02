package race

import (
	"net/http"
	"openpak/wfc-common/common"
	"openpak/wfc-common/database"
	"openpak/wfc-common/logging"
	"openpak/wfc-race/internal/rankingsapi"
	"os"
)

var (
	db database.Connection
)

func StartServer(reload bool) {
	// Get config
	config := common.GetConfig()

	common.ReadGameList()

	// Start SQL
	db = database.Start(config)
	if err := db.EnsureMarioKartWiiGhostArchive(); err != nil {
		logging.Error("RACE", "ghost archive schema:", err)
	}
	rankingsapi.Serve(os.Getenv("RANKINGS_ADDR"), "wfc", newRankingSource(dbStore{&db}))
}

func Shutdown() {
	db.Close()
}

func RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /RaceService/NintendoRacingService.asmx", handleNintendoRacingServiceRequest)
	mux.HandleFunc("GET /api/mkw/leaderboard", handleLeaderboardJSON)
	mux.Handle("GET /api/mkw/v1/", newReadAPI(dbStore{&db}))
}
