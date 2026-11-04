package race

import (
	"context"
	"os"
	"sort"

	"openpak/wfc-common/common"
	"openpak/wfc-common/database"
	"openpak/wfc-common/logging"
	"openpak/wfc-common/mkwboard"
)

// board is the Mario Kart Wii view of the leaderboards core: the times and their ranking live
// there (filed by wfc-sake), and every ranking this server answers is read from it. It is nil
// only when WFC_LEADERBOARDS_ADDR is unset, which falls back to the game's own table.
var board *mkwboard.Board

func startBoard(cfg common.Config) {
	addr, key := os.Getenv("WFC_LEADERBOARDS_ADDR"), os.Getenv("WFC_LEADERBOARDS_KEY")
	if addr == "" {
		logging.Notice("RACE", "WFC_LEADERBOARDS_ADDR is not set: rankings come from the game's own table")
		return
	}
	lb, _, err := mkwboard.Dial(addr, key)
	if err != nil {
		logging.Error("RACE", "leaderboards dial:", err)
		return
	}
	links, _, err := mkwboard.DialCore(cfg.CoreAddress, cfg.CoreKey)
	if err != nil {
		logging.Error("RACE", "account core dial:", err)
		return
	}
	board = &mkwboard.Board{LB: lb, Links: links, CourseName: func(c int) string { return common.MarioKartWiiCourseId(c).Name() }}
}

// topTen is the ten best times of a course in a region (0 = worldwide).
func topTen(ctx context.Context, region common.MarioKartWiiLeaderboardRegionId, course common.MarioKartWiiCourseId) ([]database.MarioKartWiiTopTenRanking, error) {
	if board == nil {
		return db.GetMarioKartWiiTopTenRankings(region, course)
	}
	rows, _, err := board.Range(ctx, int(region), int(course), 0, 10)
	if err != nil {
		return nil, err
	}
	out := make([]database.MarioKartWiiTopTenRanking, 0, len(rows))
	for _, r := range rows {
		out = append(out, database.MarioKartWiiTopTenRanking{Score: r.Score, PID: int(r.PID), PlayerInfo: r.PlayerInfo})
	}
	return out, nil
}

// coreStore serves the read API from the leaderboards core. Ghost files and the profile map stay
// with the game's database (embedded dbStore).
type coreStore struct {
	dbStore
	b    *mkwboard.Board
	arch archive
}

// archive is the part of the game's database the core-backed store still reads.
type archive interface {
	MKWGhostIDs(ctx context.Context, course int, pids []int64) ([]database.MKWGhostRef, error)
	MKWUserOfProfile(ctx context.Context, pid int64) (int64, bool, error)
	MKWCourseCounts(ctx context.Context) ([]database.MKWCourseCount, error)
}

func (s coreStore) Rankings(ctx context.Context, region, course, limit, offset int) ([]database.MKWRankRow, int, error) {
	rows, total, err := s.b.Range(ctx, region, course, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	pids := make([]int64, 0, len(rows))
	for _, r := range rows {
		pids = append(pids, r.PID)
	}
	ghosts := map[[2]int64]int{}
	if course < mkwboard.Courses {
		refs, gerr := s.arch.MKWGhostIDs(ctx, course, pids)
		if gerr != nil {
			return nil, 0, gerr
		}
		for _, g := range refs { // newest first: keep the first seen
			k := [2]int64{g.PID, int64(g.Score)}
			if _, ok := ghosts[k]; !ok {
				ghosts[k] = g.ID
			}
		}
	}
	out := make([]database.MKWRankRow, 0, len(rows))
	for _, r := range rows {
		row := database.MKWRankRow{Rank: r.Position, PID: r.PID, UserID: r.UserID, Score: r.Score, Region: r.Region, PlayerInfo: r.PlayerInfo, Updated: r.Updated}
		if id, ok := ghosts[[2]int64{r.PID, int64(r.Score)}]; ok {
			row.GhostID = &id
		}
		out = append(out, row)
	}
	return out, total, nil
}

func (s coreStore) CourseCounts(ctx context.Context) ([]database.MKWCourseCount, error) {
	counts, err := s.arch.MKWCourseCounts(ctx) // ghosts come from the archive
	if err != nil {
		return nil, err
	}
	for i := range counts {
		n, err := s.b.Total(ctx, 0, counts[i].Course)
		if err != nil {
			return nil, err
		}
		counts[i].Ranked = n
	}
	return counts, nil
}

func (s coreStore) PlayerRankings(ctx context.Context, pids []int64) ([]database.MKWPlayerRankRow, error) {
	byAcct := map[string][]int64{}
	for _, pid := range pids {
		uid, ok, err := s.arch.MKWUserOfProfile(ctx, pid)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		acct, ok, err := s.b.Links.Account(ctx, uid)
		if err != nil {
			return nil, err
		}
		if ok {
			byAcct[acct] = append(byAcct[acct], pid)
		}
	}
	accounts := make([]string, 0, len(byAcct))
	for a := range byAcct {
		accounts = append(accounts, a)
	}
	out := []database.MKWPlayerRankRow{}
	for course := 0; course < mkwboard.Courses && len(accounts) > 0; course++ {
		rows, total, err := s.b.OfAccounts(ctx, 0, course, accounts)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, database.MKWPlayerRankRow{PID: r.PID, Course: course, Rank: r.Rank, Of: total, Score: r.Score})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].PID != out[j].PID {
			return out[i].PID < out[j].PID
		}
		return out[i].Course < out[j].Course
	})
	return out, nil
}

func (s coreStore) Contests(ctx context.Context, limit, offset int) ([]database.MKWContestRow, int, error) {
	ids, err := s.b.Contests(ctx)
	if err != nil {
		return nil, 0, err
	}
	var all []database.MKWContestRow
	for _, id := range ids {
		top, total, err := s.b.Range(ctx, 0, id, 0, 1)
		if err != nil {
			return nil, 0, err
		}
		if total > 0 && len(top) > 0 {
			all = append(all, database.MKWContestRow{ID: id, Ranked: total, Best: top[0].Score, Updated: top[0].Updated})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].Updated.Equal(all[j].Updated) {
			return all[i].Updated.After(all[j].Updated)
		}
		return all[i].ID > all[j].ID
	})
	lo := min(offset, len(all))
	return all[lo:min(lo+limit, len(all))], len(all), nil
}
