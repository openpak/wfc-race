package database

import (
	"time"

	"openpak/wfc-common/logging"
)

func (c *Connection) UpdateTables() {
	_, _ = c.pool.Exec(c.ctx, `

	ALTER TABLE ONLY public.users
		ADD IF NOT EXISTS last_ip_address character varying DEFAULT ''::character varying,
		ADD IF NOT EXISTS last_ingamesn character varying DEFAULT ''::character varying,
		ADD IF NOT EXISTS last_ip_at timestamp with time zone DEFAULT now(),
		ADD IF NOT EXISTS has_ban boolean DEFAULT false,
		ADD IF NOT EXISTS ban_issued timestamp without time zone,
		ADD IF NOT EXISTS ban_expires timestamp without time zone,
		ADD IF NOT EXISTS ban_reason character varying,
		ADD IF NOT EXISTS ban_reason_hidden character varying,
		ADD IF NOT EXISTS ban_moderator character varying,
		ADD IF NOT EXISTS ban_tos boolean,
		ADD IF NOT EXISTS open_host boolean DEFAULT false,
		ADD IF NOT EXISTS allow_default_keys boolean DEFAULT false;
	`)

	_, _ = c.pool.Exec(c.ctx, `

	DO $$ 
	BEGIN
    	IF (SELECT data_type FROM information_schema.columns WHERE table_name='users' AND column_name='ng_device_id') != 'ARRAY' THEN
        	ALTER TABLE public.users
            	ALTER COLUMN ng_device_id TYPE bigint[] using array[ng_device_id];
    	END IF;
	END $$;

	`)

	_, _ = c.pool.Exec(c.ctx, `

	ALTER TABLE ONLY public.mario_kart_wii_sake
        ADD IF NOT EXISTS id serial PRIMARY KEY,
		ADD IF NOT EXISTS upload_time timestamp without time zone;
	
	`)

	// prds#131: the email and uniquenick columns held only functions of (user_id, gsbrcd); they are
	// derived now (DerivedEmail, DerivedNick). Dropped once no reader selects them.
	_, _ = c.pool.Exec(c.ctx, `ALTER TABLE ONLY public.users DROP COLUMN IF EXISTS email, DROP COLUMN IF EXISTS unique_nick`)

	go c.runIPRetention()

	_ = c.EnsurePairing()

	if err := c.EnsureMarioKartWiiGhostArchive(); err != nil {
		logging.Error("DB", "ghost archive:", err)
	}
}

// IPRetention is how long a console's last address is kept. Bans match on it, so it stays long
// enough to catch a returning offender and no longer.
const IPRetention = 90 * 24 * time.Hour

const purgeOldIPs = `UPDATE users SET last_ip_address = '', last_ingamesn = '' WHERE last_ip_address <> '' AND last_ip_at < $1`

// PurgeOldIPs blanks the last address of consoles not seen for IPRetention. Returns the rows touched.
func (c *Connection) PurgeOldIPs(now time.Time) (int64, error) {
	t, err := c.pool.Exec(c.ctx, purgeOldIPs, now.Add(-IPRetention))
	if err != nil {
		return 0, err
	}
	return t.RowsAffected(), nil
}

func (c *Connection) runIPRetention() {
	for {
		if n, err := c.PurgeOldIPs(time.Now()); err != nil {
			logging.Error("DB", "ip retention:", err)
		} else if n > 0 {
			logging.Info("DB", "ip retention: blanked", n, "addresses")
		}
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(6 * time.Hour):
		}
	}
}
