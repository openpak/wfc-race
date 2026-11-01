package race

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"openpak/wfc-common/database"
)

type fakeStore struct {
	rankings []database.MKWRankRow
	ghosts   map[int]database.MKWGhostRow
	body     []byte
	users    map[uint64][]int64
	players  []database.MKWPlayerRankRow
	fail     bool
}

func (f *fakeStore) err() error {
	if f.fail {
		return errors.New("db down")
	}
	return nil
}
func (f *fakeStore) Rankings(_ context.Context, region, course, limit, offset int) ([]database.MKWRankRow, int, error) {
	if course == 99 {
		return nil, 0, f.err()
	}
	rows := f.rankings
	if region != 0 {
		var kept []database.MKWRankRow
		for _, r := range rows {
			if r.Region == region {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	total := len(rows)
	if offset > len(rows) {
		offset = len(rows)
	}
	rows = rows[offset:]
	if limit < len(rows) {
		rows = rows[:limit]
	}
	return rows, total, f.err()
}
func (f *fakeStore) CourseCounts(context.Context) ([]database.MKWCourseCount, error) {
	return []database.MKWCourseCount{{Course: 0, Ranked: 2, Ghosts: 3}}, f.err()
}
func (f *fakeStore) Ghosts(_ context.Context, course int, owner int64, limit, offset int) ([]database.MKWGhostRow, int, error) {
	var out []database.MKWGhostRow
	for _, g := range f.ghosts {
		if g.Course == course && (owner == 0 || g.PID == owner) {
			out = append(out, g)
		}
	}
	return out, len(out), f.err()
}
func (f *fakeStore) Ghost(_ context.Context, id int) (database.MKWGhostRow, error) {
	g, ok := f.ghosts[id]
	if !ok {
		return g, database.ErrMKWNotFound
	}
	return g, f.err()
}
func (f *fakeStore) GhostBody(context.Context, int) ([]byte, error) { return f.body, nil }
func (f *fakeStore) PlayerRankings(_ context.Context, pids []int64) ([]database.MKWPlayerRankRow, error) {
	var out []database.MKWPlayerRankRow
	for _, p := range f.players {
		for _, id := range pids {
			if p.PID == id {
				out = append(out, p)
			}
		}
	}
	return out, f.err()
}
func (f *fakeStore) ProfileIDsOfUser(_ context.Context, u uint64) ([]int64, error) {
	return f.users[u], f.err()
}
func (f *fakeStore) Contests(context.Context, int, int) ([]database.MKWContestRow, int, error) {
	return []database.MKWContestRow{{ID: 40, Ranked: 1, Best: 61000, Updated: time.Unix(0, 0)}}, 1, f.err()
}

func miiInfo(name string) string {
	b := make([]byte, 0x50)
	for i, r := range name {
		b[2+i*2+1] = byte(r)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func newFake() *fakeStore {
	gid := 7
	return &fakeStore{
		rankings: []database.MKWRankRow{
			{Rank: 1, PID: 1001, Score: 83456, Region: 3, PlayerInfo: miiInfo("Ana"), GhostID: &gid},
			{Rank: 2, PID: 1002, Score: 90000, Region: 2, PlayerInfo: miiInfo("Bo")},
		},
		ghosts: map[int]database.MKWGhostRow{7: {ID: 7, PID: 1001, Course: 0, Score: 83456, MiiName: "Ana", Vehicle: 15, Character: 7,
			Controller: 2, Year: 2024, Month: 11, Day: 9, LapCount: 3, LapTimes: []int32{27100, 28200, 28156}, DriftType: 1}},
		body:    []byte("RKGDbody"),
		users:   map[uint64][]int64{555: {1001, 1003}},
		players: []database.MKWPlayerRankRow{{PID: 1001, Course: 0, Rank: 1, Of: 2, Score: 83456}},
	}
}

func get(t *testing.T, h http.Handler, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec, m
}

func TestReadAPI(t *testing.T) {
	h := newReadAPI(newFake())

	rec, m := get(t, h, "/api/mkw/v1/courses")
	items := m["items"].([]any)
	if rec.Code != 200 || len(items) != 1 || items[0].(map[string]any)["ghosts"].(float64) != 3 || items[0].(map[string]any)["name"] != "Mario Circuit" {
		t.Fatalf("courses %d %s", rec.Code, rec.Body)
	}

	rec, m = get(t, h, "/api/mkw/v1/courses/0/rankings")
	if rec.Code != 200 || m["total"].(float64) != 2 || m["limit"].(float64) != 20 {
		t.Fatalf("rankings %s", rec.Body)
	}
	first := m["items"].([]any)[0].(map[string]any)
	if first["player"] != "1001" || first["name"] != "Ana" || first["time"] != "1:23.456" || first["time_ms"].(float64) != 83456 || first["ghost"].(float64) != 7 {
		t.Fatalf("first %v", first)
	}

	_, m = get(t, h, "/api/mkw/v1/courses/0/rankings?region=2")
	if m["total"].(float64) != 1 {
		t.Fatalf("region filter %v", m)
	}
	_, m = get(t, h, "/api/mkw/v1/courses/0/rankings?limit=1&offset=1")
	if len(m["items"].([]any)) != 1 || m["items"].([]any)[0].(map[string]any)["rank"].(float64) != 2 {
		t.Fatalf("paging %v", m)
	}
	_, m = get(t, h, "/api/mkw/v1/courses/0/rankings?limit=5000")
	if m["limit"].(float64) != 100 {
		t.Fatalf("cap %v", m)
	}

	for path, want := range map[string]int{
		"/api/mkw/v1/courses/x/rankings":            400,
		"/api/mkw/v1/courses/40000/rankings":        400,
		"/api/mkw/v1/courses/0/rankings?region=9":   400,
		"/api/mkw/v1/courses/0/rankings?limit=-1":   400,
		"/api/mkw/v1/courses/40/ghosts":             400,
		"/api/mkw/v1/courses/0/ghosts?owner=abc":    400,
		"/api/mkw/v1/objects/99":                    404,
		"/api/mkw/v1/objects/99/body":               404,
		"/api/mkw/v1/objects/zz":                    400,
		"/api/mkw/v1/players/abc/rankings":          400,
		"/api/mkw/v1/users/-3/rankings":             400,
		"/api/mkw/v1/tournaments/5":                 400,
		"/api/mkw/v1/courses/0/rankings?offset=abc": 400,
	} {
		rec, _ := get(t, h, path)
		if rec.Code != want {
			t.Errorf("%s = %d, want %d (%s)", path, rec.Code, want, rec.Body)
		}
	}

	rec, m = get(t, h, "/api/mkw/v1/courses/0/ghosts?owner=1001")
	g := m["items"].([]any)[0].(map[string]any)
	if rec.Code != 200 || g["id"].(float64) != 7 || g["owner"] != "1001" || g["data_type"].(float64) != 0 || g["vehicle_name"] != "Blue Falcon" ||
		g["character_name"] != "Luigi" || g["date"] != "2024-11-09" || g["drift_type"] != "automatic" || g["name"] != "Ana" || len(g["laps_ms"].([]any)) != 3 {
		t.Fatalf("ghost %s", rec.Body)
	}

	rec, _ = get(t, h, "/api/mkw/v1/objects/7")
	if rec.Code != 200 {
		t.Fatalf("object %d", rec.Code)
	}
	rec, _ = get(t, h, "/api/mkw/v1/objects/7/body")
	if rec.Code != 200 || rec.Body.String() != "RKGDbody" || !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment;") || !strings.HasSuffix(rec.Header().Get("Content-Disposition"), `-7.rkg"`) {
		t.Fatalf("body %d %v", rec.Code, rec.Header())
	}

	_, m = get(t, h, "/api/mkw/v1/players/1001/rankings")
	if len(m["items"].([]any)) != 1 || m["items"].([]any)[0].(map[string]any)["of"].(float64) != 2 {
		t.Fatalf("player %v", m)
	}
	_, m = get(t, h, "/api/mkw/v1/users/555/rankings")
	if len(m["players"].([]any)) != 2 || len(m["items"].([]any)) != 1 || m["user_id"] != "555" {
		t.Fatalf("user %v", m)
	}
	_, m = get(t, h, "/api/mkw/v1/users/777/rankings")
	if len(m["players"].([]any)) != 0 || len(m["items"].([]any)) != 0 {
		t.Fatalf("unknown user %v", m)
	}

	if rec, _ = get(t, h, "/api/mkw/v1/tournaments/40"); rec.Code != 200 {
		t.Fatalf("tournament %d", rec.Code)
	}
	_, m = get(t, h, "/api/mkw/v1/tournaments")
	if m["total"].(float64) != 1 {
		t.Fatalf("tournaments %v", m)
	}
}

func TestReadAPIStoreDown(t *testing.T) {
	f := newFake()
	f.fail = true
	rec, m := get(t, newReadAPI(f), "/api/mkw/v1/courses")
	if rec.Code != 503 || m["error"] != "store unavailable" {
		t.Fatalf("%d %v", rec.Code, m)
	}
}
