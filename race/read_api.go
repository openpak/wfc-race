package race

// Read-only JSON API over the Mario Kart Wii data this server holds, for the OpenKart site
// (github.com/openpak/mktv). Same shape as the Mario Kart 8 read API: lists are
// {total, offset, limit, items}, limit defaults to 20 and is capped at 100, profile ids are
// decimal strings, errors are {"error": "..."} with 400/404/503. It sits behind the same
// internal key as everything else on this listener (service.Guard in main) and is never
// published.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"openpak/wfc-common/common"
	"openpak/wfc-common/database"
	"openpak/wfc-common/logging"
)

const (
	apiDefaultLimit = 20
	apiMaxLimit     = 100
	apiMaxCourse    = 32767 // the game accepts course ids up to this for competitions
)

// readStore is what the handlers need; the Postgres implementation is dbStore.
type readStore interface {
	Rankings(ctx context.Context, region, course, limit, offset int) ([]database.MKWRankRow, int, error)
	CourseCounts(ctx context.Context) ([]database.MKWCourseCount, error)
	Ghosts(ctx context.Context, course int, owner int64, limit, offset int) ([]database.MKWGhostRow, int, error)
	Ghost(ctx context.Context, id int) (database.MKWGhostRow, error)
	GhostBody(ctx context.Context, id int) ([]byte, error)
	PlayerRankings(ctx context.Context, pids []int64) ([]database.MKWPlayerRankRow, error)
	ProfileIDsOfUser(ctx context.Context, userID uint64) ([]int64, error)
	Contests(ctx context.Context, limit, offset int) ([]database.MKWContestRow, int, error)
}

type dbStore struct{ c *database.Connection }

func (s dbStore) Rankings(ctx context.Context, region, course, limit, offset int) ([]database.MKWRankRow, int, error) {
	return s.c.MKWRankings(ctx, region, course, limit, offset)
}
func (s dbStore) CourseCounts(ctx context.Context) ([]database.MKWCourseCount, error) {
	return s.c.MKWCourseCounts(ctx)
}
func (s dbStore) Ghosts(ctx context.Context, course int, owner int64, limit, offset int) ([]database.MKWGhostRow, int, error) {
	return s.c.MKWGhosts(ctx, course, owner, limit, offset)
}
func (s dbStore) Ghost(ctx context.Context, id int) (database.MKWGhostRow, error) {
	return s.c.MKWGhost(ctx, id)
}
func (s dbStore) GhostBody(ctx context.Context, id int) ([]byte, error) {
	return s.c.MKWGhostBody(ctx, id)
}
func (s dbStore) PlayerRankings(ctx context.Context, pids []int64) ([]database.MKWPlayerRankRow, error) {
	return s.c.MKWPlayerRankings(ctx, pids)
}
func (s dbStore) ProfileIDsOfUser(ctx context.Context, userID uint64) ([]int64, error) {
	return s.c.MKWProfileIDsOfUser(ctx, userID)
}
func (s dbStore) Contests(ctx context.Context, limit, offset int) ([]database.MKWContestRow, int, error) {
	return s.c.MKWContests(ctx, limit, offset)
}

type readAPI struct {
	store readStore
	mux   *http.ServeMux
}

func newReadAPI(store readStore) http.Handler {
	a := &readAPI{store: store, mux: http.NewServeMux()}
	a.mux.HandleFunc("GET /api/mkw/v1/courses", a.courses)
	a.mux.HandleFunc("GET /api/mkw/v1/courses/{course}/rankings", a.rankings)
	a.mux.HandleFunc("GET /api/mkw/v1/courses/{course}/ghosts", a.ghosts)
	a.mux.HandleFunc("GET /api/mkw/v1/objects/{id}", a.object)
	a.mux.HandleFunc("GET /api/mkw/v1/objects/{id}/body", a.body)
	a.mux.HandleFunc("GET /api/mkw/v1/players/{pid}/rankings", a.playerRankings)
	a.mux.HandleFunc("GET /api/mkw/v1/users/{userid}/rankings", a.userRankings)
	a.mux.HandleFunc("GET /api/mkw/v1/tournaments", a.tournaments)
	a.mux.HandleFunc("GET /api/mkw/v1/tournaments/{id}", a.tournament)
	return a
}

func (a *readAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	a.mux.ServeHTTP(w, r)
}

type apiPage struct {
	Total  int `json:"total"`
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
	Items  any `json:"items"`
}

type apiCourse struct {
	Course int    `json:"course"`
	Name   string `json:"name"`
	Ranked int    `json:"ranked"`
	Ghosts int    `json:"ghosts"`
}

type apiRank struct {
	Rank    int       `json:"rank"`
	Player  string    `json:"player"`
	TimeMS  int       `json:"time_ms"`
	Time    string    `json:"time"`
	Updated time.Time `json:"updated"`
	Name    string    `json:"name,omitempty"`
	Region  int       `json:"region"`
	Ghost   *int      `json:"ghost,omitempty"` // object id of the stored ghost
}

type apiPlayerRank struct {
	Player string `json:"player"`
	Course int    `json:"course"`
	Rank   int    `json:"rank"`
	Of     int    `json:"of"`
	TimeMS int    `json:"time_ms"`
	Time   string `json:"time"`
}

// apiObject is a stored ghost: the MK8 object fields the site already reads, plus ghost fields.
type apiObject struct {
	ID        int       `json:"id"`
	Owner     string    `json:"owner"`
	Name      string    `json:"name"`
	Size      int       `json:"size"`
	DataType  int       `json:"data_type"` // the course
	Slot      *int      `json:"slot"`
	Tags      []string  `json:"tags"`
	MetaSize  int       `json:"meta_size"`
	MetaHex   string    `json:"meta_hex,omitempty"`
	Thumbnail *int      `json:"thumbnail"`
	Rating    *struct{} `json:"rating"`
	YouTubeID string    `json:"youtube_id,omitempty"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`

	Course      int    `json:"course"`
	CourseName  string `json:"course_name"`
	TimeMS      int    `json:"time_ms"`
	Time        string `json:"time"`
	Region      int    `json:"region"`
	Vehicle     int    `json:"vehicle"`
	VehicleName string `json:"vehicle_name"`
	Character   int    `json:"character"`
	CharName    string `json:"character_name"`
	Controller  int    `json:"controller"`
	ControlName string `json:"controller_name"`
	Date        string `json:"date,omitempty"` // when the run was set, from the RKG header
	Laps        []int  `json:"laps_ms"`
	DriftType   string `json:"drift_type"`
	GhostType   int    `json:"ghost_type"`
	LapCount    int    `json:"lap_count"`
	Country     int    `json:"country_code"`
	State       int    `json:"state_code"`
	Location    int    `json:"location_code"`
}

type apiContest struct {
	ID       int       `json:"id"`
	Ranked   int       `json:"ranked"`
	BestMS   int       `json:"best_ms"`
	Best     string    `json:"best"`
	Updated  time.Time `json:"updated"`
	Rankings any       `json:"rankings,omitempty"`
}

var errBadRequest = errors.New("bad request")

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=15")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func storeError(w http.ResponseWriter, err error) {
	if errors.Is(err, database.ErrMKWNotFound) {
		apiError(w, http.StatusNotFound, "not found")
		return
	}
	logging.Error("RACE", "read api:", err)
	apiError(w, http.StatusServiceUnavailable, "store unavailable")
}

func paging(w http.ResponseWriter, r *http.Request) (limit, offset int, ok bool) {
	limit, offset = apiDefaultLimit, 0
	for name, dst := range map[string]*int{"limit": &limit, "offset": &offset} {
		if v := r.URL.Query().Get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				apiError(w, http.StatusBadRequest, name+" must be a non-negative integer")
				return 0, 0, false
			}
			*dst = n
		}
	}
	if limit == 0 {
		limit = apiDefaultLimit
	}
	if limit > apiMaxLimit {
		limit = apiMaxLimit
	}
	return limit, offset, true
}

func digits(s string) bool { return s != "" && len(s) <= 20 && strings.Trim(s, "0123456789") == "" }

func pathCourse(w http.ResponseWriter, r *http.Request, max int) (int, bool) {
	n, err := strconv.Atoi(r.PathValue("course"))
	if err != nil || n < 0 || n > max {
		apiError(w, http.StatusBadRequest, fmt.Sprintf("course must be 0-%d", max))
		return 0, false
	}
	return n, true
}

func regionParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	v := r.URL.Query().Get("region")
	if v == "" {
		return common.Worldwide, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || !common.MarioKartWiiLeaderboardRegionId(n).IsValid() {
		apiError(w, http.StatusBadRequest, "region must be 0-7")
		return 0, false
	}
	return n, true
}

func (a *readAPI) courses(w http.ResponseWriter, r *http.Request) {
	counts, err := a.store.CourseCounts(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	items := make([]apiCourse, 0, len(counts))
	for _, c := range counts {
		items = append(items, apiCourse{Course: c.Course, Name: common.MarioKartWiiCourseId(c.Course).Name(), Ranked: c.Ranked, Ghosts: c.Ghosts})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func rankItems(rows []database.MKWRankRow) []apiRank {
	items := make([]apiRank, 0, len(rows))
	for _, row := range rows {
		info, _ := base64.StdEncoding.DecodeString(row.PlayerInfo)
		items = append(items, apiRank{
			Rank: row.Rank, Player: strconv.FormatInt(row.PID, 10), TimeMS: row.Score, Time: common.FormatMKWTime(row.Score),
			Updated: row.Updated, Name: common.MiiNameFromData(info), Region: row.Region, Ghost: row.GhostID,
		})
	}
	return items
}

func (a *readAPI) rankings(w http.ResponseWriter, r *http.Request) {
	a.rankingsFor(w, r, apiMaxCourse)
}

func (a *readAPI) rankingsFor(w http.ResponseWriter, r *http.Request, max int) {
	course, ok := pathCourse(w, r, max)
	if !ok {
		return
	}
	limit, offset, ok := paging(w, r)
	if !ok {
		return
	}
	region, ok := regionParam(w, r)
	if !ok {
		return
	}
	rows, total, err := a.store.Rankings(r.Context(), region, course, limit, offset)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiPage{Total: total, Offset: offset, Limit: limit, Items: rankItems(rows)})
}

func ghostObject(g database.MKWGhostRow) apiObject {
	laps := make([]int, len(g.LapTimes))
	for i, v := range g.LapTimes {
		laps[i] = int(v)
	}
	drift := "manual"
	if g.DriftType == 1 {
		drift = "automatic"
	}
	date := ""
	if g.Month >= 1 && g.Month <= 12 && g.Day >= 1 && g.Day <= 31 {
		date = fmt.Sprintf("%04d-%02d-%02d", g.Year, g.Month, g.Day)
	}
	course, vehicle, char, ctrl := common.MarioKartWiiCourseId(g.Course), common.MarioKartWiiVehicleId(g.Vehicle),
		common.MarioKartWiiCharacterId(g.Character), common.MarioKartWiiControllerId(g.Controller)
	tags := []string{}
	for _, t := range []string{vehicle.Name(), char.Name(), ctrl.Name()} {
		if t != "" {
			tags = append(tags, t)
		}
	}
	name := g.MiiName
	if name == "" {
		info, _ := base64.StdEncoding.DecodeString(g.PlayerInfo)
		name = common.MiiNameFromData(info)
	}
	return apiObject{
		ID: g.ID, Owner: strconv.FormatInt(g.PID, 10), Name: name, Size: g.Size, DataType: g.Course, Tags: tags,
		MetaSize: 0, Created: g.Uploaded, Updated: g.Uploaded,
		Course: g.Course, CourseName: course.Name(), TimeMS: g.Score, Time: common.FormatMKWTime(g.Score), Region: g.Region,
		Vehicle: g.Vehicle, VehicleName: vehicle.Name(), Character: g.Character, CharName: char.Name(),
		Controller: g.Controller, ControlName: ctrl.Name(), Date: date, Laps: laps, DriftType: drift,
		GhostType: g.GhostType, LapCount: g.LapCount, Country: g.Country, State: g.State, Location: g.Location,
	}
}

func (a *readAPI) ghosts(w http.ResponseWriter, r *http.Request) {
	course, ok := pathCourse(w, r, 31) // only 0-31 have ghost bodies
	if !ok {
		return
	}
	limit, offset, ok := paging(w, r)
	if !ok {
		return
	}
	var owner int64
	if o := r.URL.Query().Get("owner"); o != "" {
		n, err := strconv.ParseInt(o, 10, 64)
		if !digits(o) || err != nil {
			apiError(w, http.StatusBadRequest, "owner must be a profile id")
			return
		}
		owner = n
	}
	rows, total, err := a.store.Ghosts(r.Context(), course, owner, limit, offset)
	if err != nil {
		storeError(w, err)
		return
	}
	items := make([]apiObject, 0, len(rows))
	for _, g := range rows {
		items = append(items, ghostObject(g))
	}
	writeJSON(w, http.StatusOK, apiPage{Total: total, Offset: offset, Limit: limit, Items: items})
}

func pathID(w http.ResponseWriter, r *http.Request) (int, bool) {
	n, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || n <= 0 {
		apiError(w, http.StatusBadRequest, "id must be a number")
		return 0, false
	}
	return n, true
}

func (a *readAPI) object(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	g, err := a.store.Ghost(r.Context(), id)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ghostObject(g))
}

// body is the stored RKG file, byte for byte, as an attachment.
func (a *readAPI) body(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	g, err := a.store.Ghost(r.Context(), id)
	if err != nil {
		storeError(w, err)
		return
	}
	data, err := a.store.GhostBody(r.Context(), id)
	if err != nil {
		storeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="mkw-%02d-%s-%d.rkg"`, g.Course, strings.NewReplacer(":", "m", ".", "s").Replace(common.FormatMKWTime(g.Score)), g.ID))
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

func playerItems(rows []database.MKWPlayerRankRow) []apiPlayerRank {
	items := make([]apiPlayerRank, 0, len(rows))
	for _, p := range rows {
		items = append(items, apiPlayerRank{Player: strconv.FormatInt(p.PID, 10), Course: p.Course, Rank: p.Rank, Of: p.Of,
			TimeMS: p.Score, Time: common.FormatMKWTime(p.Score)})
	}
	return items
}

func (a *readAPI) playerRankings(w http.ResponseWriter, r *http.Request) {
	s := r.PathValue("pid")
	pid, err := strconv.ParseInt(s, 10, 64)
	if !digits(s) || err != nil {
		apiError(w, http.StatusBadRequest, "pid must be a number")
		return
	}
	rows, err := a.store.PlayerRankings(r.Context(), []int64{pid})
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": playerItems(rows)})
}

// userRankings resolves a console user id (the subject of the core's "wfc" link) to every
// profile id of that console and returns their standings.
func (a *readAPI) userRankings(w http.ResponseWriter, r *http.Request) {
	s := r.PathValue("userid")
	uid, err := strconv.ParseUint(s, 10, 64)
	if !digits(s) || err != nil {
		apiError(w, http.StatusBadRequest, "userid must be a number")
		return
	}
	pids, err := a.store.ProfileIDsOfUser(r.Context(), uid)
	if err != nil {
		storeError(w, err)
		return
	}
	rows := []database.MKWPlayerRankRow{}
	if len(pids) > 0 {
		if rows, err = a.store.PlayerRankings(r.Context(), pids); err != nil {
			storeError(w, err)
			return
		}
	}
	players := make([]string, 0, len(pids))
	for _, p := range pids {
		players = append(players, strconv.FormatInt(p, 10))
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": s, "players": players, "items": playerItems(rows)})
}

// tournaments lists competitions: the game ranks them under course ids above 31.
func (a *readAPI) tournaments(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := paging(w, r)
	if !ok {
		return
	}
	rows, total, err := a.store.Contests(r.Context(), limit, offset)
	if err != nil {
		storeError(w, err)
		return
	}
	items := make([]apiContest, 0, len(rows))
	for _, c := range rows {
		items = append(items, apiContest{ID: c.ID, Ranked: c.Ranked, BestMS: c.Best, Best: common.FormatMKWTime(c.Best), Updated: c.Updated})
	}
	writeJSON(w, http.StatusOK, apiPage{Total: total, Offset: offset, Limit: limit, Items: items})
}

// tournament is one competition with the top 100. The game has no seasons for these, so ?season= is accepted and ignored.
func (a *readAPI) tournament(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 32 || id > apiMaxCourse {
		apiError(w, http.StatusBadRequest, "tournament id must be 32-32767")
		return
	}
	rows, total, err := a.store.Rankings(r.Context(), common.Worldwide, id, apiMaxLimit, 0)
	if err != nil {
		storeError(w, err)
		return
	}
	if total == 0 {
		apiError(w, http.StatusNotFound, "not found")
		return
	}
	c := apiContest{ID: id, Ranked: total, Rankings: rankItems(rows)}
	if len(rows) > 0 {
		c.BestMS, c.Best, c.Updated = rows[0].Score, common.FormatMKWTime(rows[0].Score), rows[0].Updated
	}
	writeJSON(w, http.StatusOK, c)
}
