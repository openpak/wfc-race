package race

import (
	"context"
	"fmt"
	"strconv"

	"openpak/wfc-common/common"
	"openpak/wfc-common/database"
	"openpak/wfc-race/internal/rankingsapi"
)

// The rankings contract over the Mario Kart Wii time-trial table (mario_kart_wii_sake): one
// worldwide board per course, id = the course number (0-31). Rows carry pid = users.user_id, the
// console the profile belongs to: it is the subject the account core links a Wii under (namespace
// "wfc"), which is how the website finds the player's name. A profile whose console is gone has
// no pid and shows as "Player". The per-region boards stay on the older /api/mkw/v1 API.

type rankStore interface {
	Rankings(ctx context.Context, region, course, limit, offset int) ([]database.MKWRankRow, int, error)
	RankOfUser(ctx context.Context, region, course int, userID int64) (int, bool, error)
}

func (s dbStore) RankOfUser(ctx context.Context, region, course int, userID int64) (int, bool, error) {
	return s.c.MKWRankOfUser(ctx, region, course, userID)
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

func parseBoard(id string) (course int, ok bool) {
	course, err := strconv.Atoi(id)
	if err != nil || !common.MarioKartWiiCourseId(course).IsValid() {
		return 0, false
	}
	return course, true
}

func (rankingSource) Boards(context.Context) ([]rankingsapi.Board, error) {
	var boards []rankingsapi.Board
	for course := common.MarioCircuit; course <= common.GBAShyGuyBeach; course++ {
		boards = append(boards, rankingsapi.Board{
			ID: boardID(common.Worldwide, course), Name: courseName(course),
			Kind: rankingsapi.KindTime, Order: rankingsapi.OrderAsc, Unit: "ms",
		})
	}
	return boards, nil
}

func toRows(rows []database.MKWRankRow) []rankingsapi.Row {
	out := make([]rankingsapi.Row, 0, len(rows))
	for _, r := range rows {
		pid := ""
		if r.UserID != 0 {
			pid = strconv.FormatInt(r.UserID, 10)
		}
		out = append(out, rankingsapi.Row{
			Rank: r.Rank, PID: pid, Score: int64(r.Score),
			Display: rankingsapi.Time(int64(r.Score)), UpdatedAt: r.Updated.UTC(),
		})
	}
	return out
}

func (s rankingSource) Rows(ctx context.Context, id string, q rankingsapi.Query) (rankingsapi.Page, error) {
	course, ok := parseBoard(id)
	if !ok {
		return rankingsapi.Page{}, rankingsapi.ErrNoBoard
	}
	const region = common.Worldwide
	rows, total, err := s.store.Rankings(ctx, region, course, q.Limit, q.Offset)
	if err != nil {
		return rankingsapi.Page{}, err
	}
	p := rankingsapi.Page{Total: total, Rows: toRows(rows)}
	if user, err := strconv.ParseInt(q.Around, 10, 64); err == nil && q.Around != "" {
		rank, ranked, err := s.store.RankOfUser(ctx, region, course, user)
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
