package database

import (
	"context"
	"errors"
	"time"

	"openpak/wfc-common/common"
	"openpak/wfc-common/logging"

	"github.com/jackc/pgx/v5"
)

// The game's own ranking needs one best ghost per player and course, which is what
// mario_kart_wii_sake keeps. mario_kart_wii_ghosts keeps every uploaded ghost (with the
// metadata read from its RKG header) for the read API. Nothing the game reads comes from it.

const createGhostArchive = `
CREATE TABLE IF NOT EXISTS mario_kart_wii_ghosts (
	id serial PRIMARY KEY,
	courseid integer NOT NULL,
	pid bigint NOT NULL,
	regionid integer NOT NULL,
	score integer NOT NULL,
	upload_time timestamp without time zone NOT NULL,
	playerinfo text NOT NULL DEFAULT '',
	ghost bytea NOT NULL,
	mii_name text NOT NULL DEFAULT '',
	vehicle integer NOT NULL DEFAULT 0,
	character integer NOT NULL DEFAULT 0,
	controller integer NOT NULL DEFAULT 0,
	ghost_year integer NOT NULL DEFAULT 0,
	ghost_month integer NOT NULL DEFAULT 0,
	ghost_day integer NOT NULL DEFAULT 0,
	ghost_type integer NOT NULL DEFAULT 0,
	drift_type integer NOT NULL DEFAULT 0,
	lap_count integer NOT NULL DEFAULT 0,
	lap_times integer[] NOT NULL DEFAULT '{}',
	country integer NOT NULL DEFAULT 0,
	state integer NOT NULL DEFAULT 0,
	location integer NOT NULL DEFAULT 0,
	CONSTRAINT mario_kart_wii_ghosts_upload UNIQUE (courseid, pid, score, upload_time)
);
CREATE INDEX IF NOT EXISTS mario_kart_wii_ghosts_course_score ON mario_kart_wii_ghosts (courseid, score);
CREATE INDEX IF NOT EXISTS mario_kart_wii_ghosts_pid ON mario_kart_wii_ghosts (pid);
`

// archiveGhostStatement copies the player info and upload time from the live row, so a
// backfilled row and a new one carry the same key.
const archiveGhostStatement = `
INSERT INTO mario_kart_wii_ghosts
	(courseid, pid, regionid, score, upload_time, playerinfo, ghost, mii_name, vehicle, character, controller,
	 ghost_year, ghost_month, ghost_day, ghost_type, drift_type, lap_count, lap_times, country, state, location)
SELECT s.courseid, s.pid, s.regionid, s.score, COALESCE(s.upload_time, 'epoch'::timestamp), s.playerinfo, $3,
	$4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
FROM mario_kart_wii_sake s
WHERE s.courseid = $1 AND s.pid = $2
ON CONFLICT DO NOTHING`

const backfillCandidates = `
SELECT s.courseid, s.pid, s.ghost
FROM mario_kart_wii_sake s
WHERE s.ghost IS NOT NULL AND octet_length(s.ghost) > 0
AND NOT EXISTS (SELECT 1 FROM mario_kart_wii_ghosts g
	WHERE g.courseid = s.courseid AND g.pid = s.pid AND g.score = s.score
	AND g.upload_time = COALESCE(s.upload_time, 'epoch'::timestamp))`

// EnsureMarioKartWiiGhostArchive creates the archive table if it is missing and copies the
// ghosts already in mario_kart_wii_sake into it. It is idempotent and safe to run on every
// start by any service that touches the table.
func (c *Connection) EnsureMarioKartWiiGhostArchive() error {
	if _, err := c.pool.Exec(c.ctx, createGhostArchive); err != nil {
		return err
	}
	rows, err := c.pool.Query(c.ctx, backfillCandidates)
	if err != nil {
		return err
	}
	type cand struct {
		course, pid int
		ghost       []byte
	}
	var todo []cand
	for rows.Next() {
		var k cand
		if err := rows.Scan(&k.course, &k.pid, &k.ghost); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range todo {
		if err := c.archiveGhost(k.course, k.pid, k.ghost); err != nil {
			logging.Error("DB", "ghost archive backfill:", err)
		}
	}
	if len(todo) > 0 {
		logging.Info("DB", "archived existing Mario Kart Wii ghosts:", len(todo))
	}
	return nil
}

func (c *Connection) archiveGhost(course, pid int, ghost []byte) error {
	m, err := common.ParseRKGMetadata(ghost)
	if err != nil {
		return err
	}
	laps := make([]int32, len(m.LapTimesMS))
	for i, v := range m.LapTimesMS {
		laps[i] = int32(v)
	}
	_, err = c.pool.Exec(c.ctx, archiveGhostStatement, course, pid, ghost, m.MiiName, int(m.Vehicle), int(m.Character),
		int(m.Controller), m.Year, m.Month, m.Day, m.GhostType, m.DriftType, m.LapCount, laps,
		int(m.CountryCode), int(m.StateCode), int(m.LocationCode))
	return err
}

// MKWGhostRow is one archived ghost with its header metadata.
type MKWGhostRow struct {
	ID         int
	PID        int64
	Course     int
	Region     int
	Score      int
	Uploaded   time.Time
	PlayerInfo string
	Size       int
	MiiName    string
	Vehicle    int
	Character  int
	Controller int
	Year       int
	Month      int
	Day        int
	GhostType  int
	DriftType  int
	LapCount   int
	LapTimes   []int32
	Country    int
	State      int
	Location   int
}

// MKWRankRow is one place in a course ranking.
type MKWRankRow struct {
	Rank       int
	PID        int64
	Score      int
	Region     int
	PlayerInfo string
	Updated    time.Time
	GhostID    *int // newest archived ghost with this exact time, when its body was kept
}

// MKWPlayerRankRow is one player's standing on one course.
type MKWPlayerRankRow struct {
	PID    int64
	Course int
	Rank   int
	Of     int
	Score  int
}

// MKWCourseCount is how many players are ranked on a course and how many ghosts are archived.
type MKWCourseCount struct {
	Course int
	Ranked int
	Ghosts int
}

// MKWContestRow is a competition (course ids above 31, uploads with the contest flag).
type MKWContestRow struct {
	ID      int
	Ranked  int
	Best    int
	Updated time.Time
}

var ErrMKWNotFound = errors.New("not found")

const ghostColumns = `g.id, g.pid, g.courseid, g.regionid, g.score, g.upload_time, g.playerinfo, octet_length(g.ghost),
	g.mii_name, g.vehicle, g.character, g.controller, g.ghost_year, g.ghost_month, g.ghost_day, g.ghost_type,
	g.drift_type, g.lap_count, g.lap_times, g.country, g.state, g.location`

func scanGhost(row pgx.Row) (MKWGhostRow, error) {
	var g MKWGhostRow
	err := row.Scan(&g.ID, &g.PID, &g.Course, &g.Region, &g.Score, &g.Uploaded, &g.PlayerInfo, &g.Size, &g.MiiName,
		&g.Vehicle, &g.Character, &g.Controller, &g.Year, &g.Month, &g.Day, &g.GhostType, &g.DriftType, &g.LapCount,
		&g.LapTimes, &g.Country, &g.State, &g.Location)
	return g, err
}

// MKWRankings is the full ranking of a course (region 0 = worldwide), fastest first, one row
// per player: the game's own table.
func (c *Connection) MKWRankings(ctx context.Context, region, course, limit, offset int) ([]MKWRankRow, int, error) {
	var total int
	if err := c.pool.QueryRow(ctx, `SELECT count(*) FROM mario_kart_wii_sake WHERE ($1 = 0 OR regionid = $1) AND courseid = $2`,
		region, course).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := c.pool.Query(ctx, `SELECT score, pid, regionid, playerinfo, COALESCE(upload_time, 'epoch'::timestamp),
		(SELECT g.id FROM mario_kart_wii_ghosts g WHERE g.courseid = s.courseid AND g.pid = s.pid AND g.score = s.score
			ORDER BY g.id DESC LIMIT 1)
		FROM mario_kart_wii_sake s WHERE ($1 = 0 OR regionid = $1) AND courseid = $2
		ORDER BY score ASC, upload_time ASC NULLS FIRST, pid ASC LIMIT $3 OFFSET $4`, region, course, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []MKWRankRow{}
	for rows.Next() {
		r := MKWRankRow{Rank: offset + len(out) + 1}
		if err := rows.Scan(&r.Score, &r.PID, &r.Region, &r.PlayerInfo, &r.Updated, &r.GhostID); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// MKWCourseCounts counts ranked players and archived ghosts per course id below 32.
func (c *Connection) MKWCourseCounts(ctx context.Context) ([]MKWCourseCount, error) {
	rows, err := c.pool.Query(ctx, `SELECT c, COALESCE(r.n, 0), COALESCE(g.n, 0)
		FROM generate_series(0, 31) c
		LEFT JOIN (SELECT courseid, count(*) n FROM mario_kart_wii_sake GROUP BY courseid) r ON r.courseid = c
		LEFT JOIN (SELECT courseid, count(*) n FROM mario_kart_wii_ghosts GROUP BY courseid) g ON g.courseid = c
		ORDER BY c`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MKWCourseCount{}
	for rows.Next() {
		var k MKWCourseCount
		if err := rows.Scan(&k.Course, &k.Ranked, &k.Ghosts); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// MKWGhosts lists archived ghosts of a course, fastest first; owner 0 means everyone.
func (c *Connection) MKWGhosts(ctx context.Context, course int, owner int64, limit, offset int) ([]MKWGhostRow, int, error) {
	var total int
	if err := c.pool.QueryRow(ctx, `SELECT count(*) FROM mario_kart_wii_ghosts g WHERE g.courseid = $1 AND ($2 = 0 OR g.pid = $2)`,
		course, owner).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := c.pool.Query(ctx, `SELECT `+ghostColumns+` FROM mario_kart_wii_ghosts g
		WHERE g.courseid = $1 AND ($2 = 0 OR g.pid = $2) ORDER BY g.score ASC, g.id ASC LIMIT $3 OFFSET $4`,
		course, owner, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []MKWGhostRow{}
	for rows.Next() {
		g, err := scanGhost(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, g)
	}
	return out, total, rows.Err()
}

// MKWGhost is one archived ghost's metadata.
func (c *Connection) MKWGhost(ctx context.Context, id int) (MKWGhostRow, error) {
	g, err := scanGhost(c.pool.QueryRow(ctx, `SELECT `+ghostColumns+` FROM mario_kart_wii_ghosts g WHERE g.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return g, ErrMKWNotFound
	}
	return g, err
}

// MKWGhostBody is the RKG file of one archived ghost.
func (c *Connection) MKWGhostBody(ctx context.Context, id int) ([]byte, error) {
	var b []byte
	err := c.pool.QueryRow(ctx, `SELECT ghost FROM mario_kart_wii_ghosts WHERE id = $1`, id).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrMKWNotFound
	}
	return b, err
}

// MKWPlayerRankings is the worldwide standing of each given profile on every course 0-31 it is ranked on.
func (c *Connection) MKWPlayerRankings(ctx context.Context, pids []int64) ([]MKWPlayerRankRow, error) {
	rows, err := c.pool.Query(ctx, `SELECT s.pid, s.courseid, s.score,
		(SELECT count(*) + 1 FROM mario_kart_wii_sake o WHERE o.courseid = s.courseid AND o.score < s.score),
		(SELECT count(*) FROM mario_kart_wii_sake o WHERE o.courseid = s.courseid)
		FROM mario_kart_wii_sake s WHERE s.pid = ANY($1) AND s.courseid BETWEEN 0 AND 31
		ORDER BY s.pid, s.courseid`, pids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MKWPlayerRankRow{}
	for rows.Next() {
		var r MKWPlayerRankRow
		if err := rows.Scan(&r.PID, &r.Course, &r.Score, &r.Rank, &r.Of); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MKWProfileIDsOfUser is every profile id (one per game) of one console user id.
func (c *Connection) MKWProfileIDsOfUser(ctx context.Context, userID uint64) ([]int64, error) {
	rows, err := c.pool.Query(ctx, `SELECT profile_id FROM users WHERE user_id = $1 ORDER BY profile_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var p int64
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MKWContests lists competitions: course ids above 31 that have ranked entries.
func (c *Connection) MKWContests(ctx context.Context, limit, offset int) ([]MKWContestRow, int, error) {
	var total int
	if err := c.pool.QueryRow(ctx, `SELECT count(DISTINCT courseid) FROM mario_kart_wii_sake WHERE courseid > 31`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := c.pool.Query(ctx, `SELECT courseid, count(*), min(score), max(COALESCE(upload_time, 'epoch'::timestamp))
		FROM mario_kart_wii_sake WHERE courseid > 31 GROUP BY courseid ORDER BY max(upload_time) DESC NULLS LAST, courseid DESC
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []MKWContestRow{}
	for rows.Next() {
		var r MKWContestRow
		if err := rows.Scan(&r.ID, &r.Ranked, &r.Best, &r.Updated); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}
