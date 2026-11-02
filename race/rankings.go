package race

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"openpak/wfc-common/common"
	"openpak/wfc-common/database"
	"openpak/wfc-race/internal/rankingsapi"
)

// The rankings contract over the Mario Kart Wii time-trial table (mario_kart_wii_sake): one board
// per course, worldwide, then the same course per region. Board ids are "<course>" for the
// worldwide board and "<region>-<course>" for a region (region 1-7, course 0-31). Rows carry
// pid, the GameSpy profile id (users.profile_id) that wfc-sake keys the table by; the console
// user id (users.user_id) is a different number and is not sent.

var mkwRegionNames = [...]string{"Worldwide", "Japan", "United States", "Europe", "Australia", "Taiwan", "Korea", "China"}

type rankStore interface {
	Rankings(ctx context.Context, region, course, limit, offset int) ([]database.MKWRankRow, int, error)
	RankOf(ctx context.Context, region, course int, pid int64) (int, bool, error)
}

func (s dbStore) RankOf(ctx context.Context, region, course int, pid int64) (int, bool, error) {
	return s.c.MKWRankOf(ctx, region, course, pid)
}

type rankingSource struct{ store rankStore }

func newRankingSource(store rankStore) rankingsapi.Source { return rankingSource{store} }

func boardID(region, course int) string {
	if region == common.Worldwide {
		return strconv.Itoa(course)
	}
	return fmt.Sprintf("%d-%d", region, course)
}

func courseName(course int) string {
	if n := common.MarioKartWiiCourseId(course).Name(); n != "" {
		return n
	}
	return rankingsapi.NeutralName(uint64(course))
}

func boardName(region, course int) string {
	if region == common.Worldwide {
		return courseName(course)
	}
	return courseName(course) + " (" + mkwRegionNames[region] + ")"
}

func parseBoard(id string) (region, course int, ok bool) {
	r, c, found := strings.Cut(id, "-")
	if !found {
		r, c = "0", id
	}
	region, err1 := strconv.Atoi(r)
	course, err2 := strconv.Atoi(c)
	if err1 != nil || err2 != nil || (found && region == common.Worldwide) ||
		!common.MarioKartWiiLeaderboardRegionId(region).IsValid() || !common.MarioKartWiiCourseId(course).IsValid() {
		return 0, 0, false
	}
	return region, course, true
}

func (rankingSource) Boards(context.Context) ([]rankingsapi.Board, error) {
	var boards []rankingsapi.Board
	for region := common.Worldwide; region <= common.China; region++ {
		for course := common.MarioCircuit; course <= common.GBAShyGuyBeach; course++ {
			boards = append(boards, rankingsapi.Board{
				ID: boardID(region, course), Name: boardName(region, course),
				Kind: rankingsapi.KindTime, Order: rankingsapi.OrderAsc, Unit: "ms",
			})
		}
	}
	return boards, nil
}

func toRows(rows []database.MKWRankRow) []rankingsapi.Row {
	out := make([]rankingsapi.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, rankingsapi.Row{
			Rank: r.Rank, PID: strconv.FormatInt(r.PID, 10), Score: int64(r.Score),
			Display: rankingsapi.Time(int64(r.Score)), UpdatedAt: r.Updated.UTC(),
		})
	}
	return out
}

func (s rankingSource) Rows(ctx context.Context, id string, q rankingsapi.Query) (rankingsapi.Page, error) {
	region, course, ok := parseBoard(id)
	if !ok {
		return rankingsapi.Page{}, rankingsapi.ErrNoBoard
	}
	rows, total, err := s.store.Rankings(ctx, region, course, q.Limit, q.Offset)
	if err != nil {
		return rankingsapi.Page{}, err
	}
	p := rankingsapi.Page{Total: total, Rows: toRows(rows)}
	if pid, err := strconv.ParseInt(q.Around, 10, 64); err == nil && q.Around != "" {
		rank, ranked, err := s.store.RankOf(ctx, region, course, pid)
		if err != nil {
			return rankingsapi.Page{}, err
		}
		if ranked {
			off := max(rank-1-rankingsapi.AroundSpan, 0)
			near, _, err := s.store.Rankings(ctx, region, course, 2*rankingsapi.AroundSpan+1, off)
			if err != nil {
				return rankingsapi.Page{}, err
			}
			p.Around = toRows(near)
		}
	}
	return p, nil
}
