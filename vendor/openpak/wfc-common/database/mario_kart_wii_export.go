package database

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// MKWTimeRow is one row of the game's own time table with the console user it belongs to.
type MKWTimeRow struct {
	PID        int64
	UserID     int64 // 0 when the profile's console is gone
	Region     int
	Course     int
	Score      int
	PlayerInfo string
	Uploaded   time.Time // zero when the row has no upload time
}

// MKWAllTimes lists every row of mario_kart_wii_sake, for the one-way import into the
// leaderboards core. Nothing is modified.
func (c *Connection) MKWAllTimes(ctx context.Context) ([]MKWTimeRow, error) {
	rows, err := c.pool.Query(ctx, `SELECT s.pid, COALESCE(u.user_id, 0), s.regionid, s.courseid, s.score, s.playerinfo, COALESCE(s.upload_time, 'epoch'::timestamp)
		FROM mario_kart_wii_sake s LEFT JOIN users u ON u.profile_id = s.pid
		ORDER BY s.courseid, s.score, s.pid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MKWTimeRow
	for rows.Next() {
		var r MKWTimeRow
		if err := rows.Scan(&r.PID, &r.UserID, &r.Region, &r.Course, &r.Score, &r.PlayerInfo, &r.Uploaded); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MKWUserOfProfile is the console user id of a profile, false when the profile is unknown.
func (c *Connection) MKWUserOfProfile(ctx context.Context, pid int64) (int64, bool, error) {
	var uid int64
	err := c.pool.QueryRow(ctx, `SELECT user_id FROM users WHERE profile_id = $1`, pid).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return uid, err == nil, err
}

// MKWGhostRef names the newest archived ghost of a profile with an exact time.
type MKWGhostRef struct {
	PID   int64
	Score int
	ID    int
}

// MKWGhostIDs finds, for each given profile, its archived ghosts on a course (newest first), so
// a ranking taken from the leaderboards core can link a time to its stored ghost.
func (c *Connection) MKWGhostIDs(ctx context.Context, course int, pids []int64) ([]MKWGhostRef, error) {
	if len(pids) == 0 {
		return nil, nil
	}
	rows, err := c.pool.Query(ctx, `SELECT pid, score, id FROM mario_kart_wii_ghosts
		WHERE courseid = $1 AND pid = ANY($2) AND octet_length(ghost) > 0 ORDER BY id DESC`, course, pids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MKWGhostRef
	for rows.Next() {
		var g MKWGhostRef
		if err := rows.Scan(&g.PID, &g.Score, &g.ID); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
