package database

import (
	"context"
	"fmt"
)

// EraseUsers removes everything the WFC database holds for the given console user ids
// (users.user_id, the "wfc" subject of an account-core link), in one transaction. It returns
// the number of rows deleted and is idempotent: a second run finds nothing and returns 0.
//
// Every WFC service (nas, gpcm, gpsp, gamestats, sake, race, dls, ...) shares this one
// database, so this is the single place an account's WFC data lives. Tables that an older
// deployment never created are skipped.
func (c *Connection) EraseUsers(ctx context.Context, userIDs []int64) (int, error) {
	if len(userIDs) == 0 {
		return 0, nil
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// Profile ids (one per game) of these consoles; every per-game table is keyed by them.
	rows, err := tx.Query(ctx, `SELECT profile_id FROM users WHERE user_id = ANY($1::bigint[])`, userIDs)
	if err != nil {
		return 0, err
	}
	var pids []int64
	for rows.Next() {
		var p int64
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return 0, err
		}
		pids = append(pids, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	pids32 := make([]int32, len(pids))
	for i, p := range pids {
		pids32[i] = int32(p)
	}
	pidText := make([]string, len(pids))
	for i, p := range pids {
		pidText[i] = fmt.Sprint(p)
	}

	steps := []struct {
		table, sql string
		arg        any
	}{
		{"gamestats_public_data", `DELETE FROM gamestats_public_data WHERE profile_id = ANY($1::bigint[])`, pids},
		{"sake_records", `DELETE FROM sake_records WHERE owner_id = ANY($1::integer[])`, pids32},
		{"mario_kart_wii_sake", `DELETE FROM mario_kart_wii_sake WHERE pid = ANY($1::bigint[])`, pids},
		{"mario_kart_wii_ghosts", `DELETE FROM mario_kart_wii_ghosts WHERE pid = ANY($1::bigint[])`, pids},
		{"sessions", `DELETE FROM sessions WHERE profile_id = ANY($1::bigint[])`, pids},
		{"events", `DELETE FROM events WHERE event_data->>'profile_id' = ANY($1::text[])`, pidText},
		{"wfc_pairing_codes", `DELETE FROM wfc_pairing_codes WHERE user_id = ANY($1::bigint[])`, userIDs},
		{"wfc_pairing_linked", `DELETE FROM wfc_pairing_linked WHERE user_id = ANY($1::bigint[])`, userIDs},
		{"users", `DELETE FROM users WHERE user_id = ANY($1::bigint[])`, userIDs},
	}
	total := 0
	for _, s := range steps {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, s.table).Scan(&exists); err != nil {
			return 0, err
		}
		if !exists {
			continue
		}
		tag, err := tx.Exec(ctx, s.sql, s.arg)
		if err != nil {
			return 0, fmt.Errorf("erase %s: %w", s.table, err)
		}
		total += int(tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return total, nil
}
