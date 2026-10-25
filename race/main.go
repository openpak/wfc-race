package race

import (
	"net/http"
	"openpak/wfc-common/common"
	"openpak/wfc-common/database"
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
}

func Shutdown() {
	db.Close()
}

func RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /RaceService/NintendoRacingService.asmx", handleNintendoRacingServiceRequest)
}
