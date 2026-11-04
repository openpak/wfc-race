package race

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"openpak/wfc-common/database"
	"openpak/wfc-common/mkwboard"
	"openpak/wfc-common/mkwboard/fakeleaderboards"
)

const (
	acctA = "11111111-1111-4111-8111-111111111111"
	acctB = "22222222-2222-4222-8222-222222222222"
)

type linkMap map[int64]string

func (l linkMap) Account(_ context.Context, uid int64) (string, bool, error) {
	a, ok := l[uid]
	return a, ok, nil
}

type fakeArchive struct{}

func (fakeArchive) MKWGhostIDs(_ context.Context, course int, pids []int64) ([]database.MKWGhostRef, error) {
	return []database.MKWGhostRef{{PID: 10, Score: 90000, ID: 77}}, nil
}
func (fakeArchive) MKWUserOfProfile(_ context.Context, pid int64) (int64, bool, error) {
	return pid / 10, true, nil // profile 10 -> console 1, 20 -> 2
}
func (fakeArchive) MKWCourseCounts(context.Context) ([]database.MKWCourseCount, error) {
	out := make([]database.MKWCourseCount, 32)
	for i := range out {
		out[i] = database.MKWCourseCount{Course: i, Ghosts: 1}
	}
	return out, nil
}

func newCoreAPI(t *testing.T) http.Handler {
	t.Helper()
	f := fakeleaderboards.New("k")
	cli, conn, err := mkwboard.Dial("passthrough:///bufnet", "k", f.Serve(t)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	b := &mkwboard.Board{LB: cli, Links: linkMap{1: acctA, 2: acctB}}
	ctx := context.Background()
	for _, tm := range []mkwboard.Time{
		{UserID: 1, PID: 10, Region: 3, Course: 4, Score: 90000, PlayerInfo: "aaa"},
		{UserID: 2, PID: 20, Region: 3, Course: 4, Score: 80000, PlayerInfo: "bbb"},
		{UserID: 2, PID: 20, Region: 3, Course: 40, Score: 70000},
	} {
		if err := b.Submit(ctx, tm); err != nil {
			t.Fatal(err)
		}
	}
	return newReadAPI(coreStore{b: b, arch: fakeArchive{}})
}

func getJSON(t *testing.T, h http.Handler, path string, v any) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", path, w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatal(err)
	}
}

// The OpenKart read API keeps its JSON shape when the times come from the core.
func TestReadAPIOverCore(t *testing.T) {
	h := newCoreAPI(t)
	var rk struct {
		Total int
		Items []struct {
			Rank   int
			Player string
			TimeMS int `json:"time_ms"`
			Region int
			Ghost  *int
		}
	}
	getJSON(t, h, "/api/mkw/v1/courses/4/rankings", &rk)
	if rk.Total != 2 || rk.Items[0].Player != "20" || rk.Items[0].Rank != 1 || rk.Items[1].TimeMS != 90000 || rk.Items[1].Region != 3 || rk.Items[1].Ghost == nil || *rk.Items[1].Ghost != 77 || rk.Items[0].Ghost != nil {
		t.Fatalf("%+v", rk)
	}
	getJSON(t, h, "/api/mkw/v1/courses/4/rankings?region=5", &rk)
	if rk.Total != 0 {
		t.Fatalf("region 5: %+v", rk)
	}
	var pr struct{ Items []struct{ Course, Rank, Of int } }
	getJSON(t, h, "/api/mkw/v1/players/10/rankings", &pr)
	if len(pr.Items) != 1 || pr.Items[0].Course != 4 || pr.Items[0].Rank != 2 || pr.Items[0].Of != 2 {
		t.Fatalf("player %+v", pr)
	}
	var cs struct{ Items []struct{ Course, Ranked, Ghosts int } }
	getJSON(t, h, "/api/mkw/v1/courses", &cs)
	if cs.Items[4].Ranked != 2 || cs.Items[4].Ghosts != 1 || cs.Items[0].Ranked != 0 {
		t.Fatalf("courses %+v", cs.Items[:5])
	}
	var tn struct{ Total int }
	getJSON(t, h, "/api/mkw/v1/tournaments", &tn)
	if tn.Total != 1 {
		t.Fatalf("tournaments %+v", tn)
	}
}
