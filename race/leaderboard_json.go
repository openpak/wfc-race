package race

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"openpak/wfc-common/common"
	"openpak/wfc-common/logging"
	"strconv"
	"unicode/utf16"
)

// A read-only JSON view of the Mario Kart Wii time-trial leaderboard for the website
// (GET /api/mkw/leaderboard?region=&course=). It answers what GetTopTenRankings answers, for one
// region and course, with the player's Mii name decoded. It sits behind the same internal key as
// the SOAP endpoint (service.Guard in main).

type leaderboardEntry struct {
	Rank   int    `json:"rank"`
	PID    int    `json:"pid"`
	TimeMS int    `json:"time_ms"`
	Name   string `json:"name"`
}

type leaderboardResponse struct {
	Region  int                `json:"region"`
	Course  int                `json:"course"`
	Entries []leaderboardEntry `json:"entries"`
}

// miiName is the player's Mii name from a stored player-info blob: ten UTF-16 big-endian characters
// at offset 2 of the Mii data, ended by a null.
func miiName(playerInfo []byte) string {
	if len(playerInfo) < 0x16 {
		return ""
	}
	units := make([]uint16, 0, 10)
	for i := 0; i < 10; i++ {
		u := binary.BigEndian.Uint16(playerInfo[2+i*2:])
		if u == 0 {
			break
		}
		units = append(units, u)
	}
	return string(utf16.Decode(units))
}

func handleLeaderboardJSON(w http.ResponseWriter, r *http.Request) {
	region, err1 := strconv.Atoi(r.URL.Query().Get("region"))
	course, err2 := strconv.Atoi(r.URL.Query().Get("course"))
	if r.URL.Query().Get("region") == "" {
		region, err1 = common.Worldwide, nil
	}
	regionId, courseId := common.MarioKartWiiLeaderboardRegionId(region), common.MarioKartWiiCourseId(course)
	if err1 != nil || err2 != nil || !regionId.IsValid() || !courseId.IsValid() {
		http.Error(w, `{"error":"region 0-7 and course 0-31 required"}`, http.StatusBadRequest)
		return
	}
	rows, err := db.GetMarioKartWiiTopTenRankings(regionId, courseId)
	if err != nil {
		logging.Error("RACE", "leaderboard json:", err)
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	out := leaderboardResponse{Region: region, Course: course, Entries: []leaderboardEntry{}}
	for i, row := range rows {
		info, err := base64.StdEncoding.DecodeString(row.PlayerInfo)
		if err != nil {
			info = nil
		}
		out.Entries = append(out.Entries, leaderboardEntry{Rank: i + 1, PID: row.PID, TimeMS: row.Score, Name: miiName(info)})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
