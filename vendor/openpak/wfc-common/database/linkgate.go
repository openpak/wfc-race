package database

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"openpak/wfc-common/logging"
)

// Link-first (prds repos/wfc-link-first-design.md): a console plays online only once it is
// linked to an OpenPak account. wfc-gpcm refuses an unlinked console's login and shows it its
// code; QR2, SAKE, gamestats and race refuse it too, as defence in depth. WFC_LINK_FIRST=1
// turns the gate on; off, every helper here answers "linked" and nothing changes.

// LinkFirst reports whether the link-first gate is on (WFC_LINK_FIRST=1 or true).
func LinkFirst() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WFC_LINK_FIRST"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// GateResult is the gate's answer about a console.
type GateResult int

const (
	GateLinked      GateResult = iota
	GateUnlinked               // refuse and show a code
	GateUnavailable            // cannot tell right now (22102)
)

func (r GateResult) String() string {
	switch r {
	case GateLinked:
		return "linked"
	case GateUnlinked:
		return "not_linked"
	}
	return "unavailable"
}

// CoreChecker asks the account core about one subject: the active link's id and account,
// or active=false. An error means the core did not answer.
type CoreChecker interface {
	LinkActive(ctx context.Context, subject string) (linkID, accountID string, active bool, err error)
}

// LinkGate decides whether a console may play. The local cache answers almost always; the
// core is asked only when an approval for this console was left half-done (the website made
// the core link but the cache write did not happen), and a live core link heals the cache.
func (c *Connection) LinkGate(ctx context.Context, userID uint64, core CoreChecker) (GateResult, error) {
	if _, ok, err := c.LinkedConsoleOf(ctx, userID); err != nil {
		return GateUnavailable, err
	} else if ok {
		return GateLinked, nil
	}
	p, ok, err := c.PendingApproval(ctx, userID)
	if err != nil {
		return GateUnavailable, err
	}
	if !ok || core == nil {
		return GateUnlinked, nil
	}
	linkID, account, active, err := core.LinkActive(ctx, p.Subject)
	if err != nil {
		return GateUnavailable, err
	}
	if !active || account != p.AccountID {
		return GateUnlinked, nil
	}
	if err := c.FinishApproval(ctx, p.PairingTarget, p.AccountID, linkID); err != nil {
		return GateUnavailable, err
	}
	logging.Notice("LINK", "link gate: healed user", userID, "link", linkID)
	return GateLinked, nil
}

// linkedCache remembers positive "linked" answers for a minute, as the ban gate does.
var linkedCache = struct {
	sync.Mutex
	profiles map[uint32]time.Time
	users    map[uint64]time.Time
}{profiles: map[uint32]time.Time{}, users: map[uint64]time.Time{}}

const linkedCacheTTL = time.Minute

func forgetLinkedCache() {
	linkedCache.Lock()
	linkedCache.profiles = map[uint32]time.Time{}
	linkedCache.users = map[uint64]time.Time{}
	linkedCache.Unlock()
}

// ErrNotLinked is returned by the Require helpers for an unlinked console.
var ErrNotLinked = errors.New("console not linked to an OpenPak account")

// LinkedProfile reports whether a game profile belongs to a linked console. With the gate off
// it is always true. A database error reads as "not linked" (the caller refuses; the console
// retries).
func (c *Connection) LinkedProfile(ctx context.Context, profileID uint32) bool {
	if !LinkFirst() {
		return true
	}
	now := time.Now()
	linkedCache.Lock()
	until, ok := linkedCache.profiles[profileID]
	linkedCache.Unlock()
	if ok && now.Before(until) {
		return true
	}
	var linked bool
	err := c.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN wfc_pairing_linked l ON l.user_id = u.user_id
		WHERE u.profile_id = $1)`, int64(profileID)).Scan(&linked)
	if err != nil {
		logging.Warn("LINK", "linked profile lookup:", err)
		return false
	}
	if linked {
		linkedCache.Lock()
		linkedCache.profiles[profileID] = now.Add(linkedCacheTTL)
		linkedCache.Unlock()
	}
	return linked
}

// LinkedUser reports whether a console (user id) is linked. Always true with the gate off.
func (c *Connection) LinkedUser(ctx context.Context, userID uint64) bool {
	if !LinkFirst() {
		return true
	}
	now := time.Now()
	linkedCache.Lock()
	until, ok := linkedCache.users[userID]
	linkedCache.Unlock()
	if ok && now.Before(until) {
		return true
	}
	_, linked, err := c.LinkedConsoleOf(ctx, userID)
	if err != nil {
		logging.Warn("LINK", "linked user lookup:", err)
		return false
	}
	if linked {
		linkedCache.Lock()
		linkedCache.users[userID] = now.Add(linkedCacheTTL)
		linkedCache.Unlock()
	}
	return linked
}

// ConsoleBanned reports a Terms-of-Service ban on a console that is not linked yet (it has no
// game profile to log in with), matched like LoginUserToGPCM matches: any profile of the
// console, the Wii's device id, or the address. The gate checks it before showing a code, so a
// banned console cannot be linked to a fresh account to escape its ban.
func (c *Connection) ConsoleBanned(ctx context.Context, userID uint64, deviceID uint32, ip string) (bool, string, error) {
	var reason *string
	err := c.pool.QueryRow(ctx, `SELECT ban_reason FROM users
		WHERE has_ban = true AND ban_tos = true
		  AND (user_id = $1
		    OR ($2 <> 0 AND allow_default_keys = FALSE AND ng_device_id && array[$2]::bigint[])
		    OR ($3 <> '' AND last_ip_address = $3))
		  AND (ban_expires IS NULL OR ban_expires > now() AT TIME ZONE 'UTC')
		LIMIT 1`, int64(userID), int64(deviceID), ip).Scan(&reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if reason != nil {
		return true, *reason, nil
	}
	return true, "", nil
}
