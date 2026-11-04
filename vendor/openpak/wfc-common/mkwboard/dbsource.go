package mkwboard

import (
	"context"

	"openpak/wfc-common/database"
)

// DBSource is the Source over the game's own table.
type DBSource struct{ DB *database.Connection }

// AllTimes implements Source.
func (s DBSource) AllTimes(ctx context.Context) ([]Time, error) {
	rows, err := s.DB.MKWAllTimes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Time, 0, len(rows))
	for _, r := range rows {
		out = append(out, Time{UserID: r.UserID, PID: r.PID, Region: r.Region, Course: r.Course, Score: r.Score, PlayerInfo: r.PlayerInfo, At: r.Uploaded.UTC()})
	}
	return out, nil
}
