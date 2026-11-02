package race

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"openpak/wfc-common/database"
	"openpak/wfc-race/internal/rankingsapi"
)

type fakeRanks struct {
	rows map[string][]database.MKWRankRow
}

func (f fakeRanks) Rankings(_ context.Context, region, course, limit, offset int) ([]database.MKWRankRow, int, error) {
	all := f.rows[boardID(region, course)]
	lo := min(offset, len(all))
	out := append([]database.MKWRankRow(nil), all[lo:min(lo+limit, len(all))]...)
	for i := range out {
		out[i].Rank = lo + i + 1
	}
	return out, len(all), nil
}

func (f fakeRanks) RankOf(_ context.Context, region, course int, pid int64) (int, bool, error) {
	for i, r := range f.rows[boardID(region, course)] {
		if r.PID == pid {
			return i + 1, true, nil
		}
	}
	return 0, false, nil
}

func testRanks() fakeRanks {
	var rows []database.MKWRankRow
	for i := 0; i < 30; i++ {
		rows = append(rows, database.MKWRankRow{PID: int64(1000 + i), Score: 60000 + i*100, Updated: time.Unix(1700000000, 0)})
	}
	return fakeRanks{rows: map[string][]database.MKWRankRow{"0": rows, "3-0": rows[:4]}}
}

func TestBoards(t *testing.T) {
	b, _ := newRankingSource(testRanks()).Boards(context.Background())
	if len(b) != 8*32 || b[0].ID != "0" || b[0].Name != "Mario Circuit" || b[31].ID != "31" || b[32].ID != "1-0" {
		t.Fatalf("boards: %d %+v %+v", len(b), b[0], b[32])
	}
	if b[32+0].Name != "Mario Circuit (Japan)" || b[0].Kind != "time" || b[0].Order != "asc" {
		t.Fatalf("names: %+v", b[32])
	}
}

func TestRowsJSON(t *testing.T) {
	h := rankingsapi.Handler("wfc", newRankingSource(testRanks()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/internal/rankings/boards/0?limit=5&offset=2&around=1015", nil))
	var p rankingsapi.Page
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || rec.Code != 200 {
		t.Fatalf("%d %v %s", rec.Code, err, rec.Body)
	}
	if p.Total != 30 || len(p.Rows) != 5 || p.Rows[0].Rank != 3 || p.Rows[0].PID != "1002" || p.Rows[0].Display != "1:00.200" {
		t.Fatalf("page: %+v", p)
	}
	if len(p.Around) != 11 || p.Around[0].PID != "1010" || p.Around[5].PID != "1015" {
		t.Fatalf("around: %+v", p.Around)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/internal/rankings/boards/3-0?around=1001", nil))
	json.Unmarshal(rec.Body.Bytes(), &p)
	if p.Total != 4 || len(p.Around) != 4 {
		t.Fatalf("region: %+v", p)
	}
}

func TestUnknownBoard(t *testing.T) {
	h := rankingsapi.Handler("wfc", newRankingSource(testRanks()))
	for _, id := range []string{"32", "0-1", "8-1", "x", "1-"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/internal/rankings/boards/"+id, nil))
		if rec.Code != 404 {
			t.Fatalf("%s: %d", id, rec.Code)
		}
	}
}
