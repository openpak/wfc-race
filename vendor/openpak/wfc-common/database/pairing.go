package database

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"openpak/wfc-common/logging"
)

// A Wii or DS cannot sign in, so linking one to an OpenPak account is a device-code flow
// (prds repos/wfc-link-first-design.md). An unlinked console that logs in to GPCM is refused,
// and the refusal carries its pairing code: the 8-symbol code where the game can show text
// (Mario Kart Wii), the 6-digit link number where it can only show an error number. The player
// enters either at openpak.org/link; wfc-api then makes the account-core link and marks the
// console linked here. Codes live in Postgres, expire and work once. The console is the WFC
// user_id; wfc_pairing_linked is this family's cache of its active core link.

const (
	// PairingTTL is how long a code stays valid after it is issued.
	PairingTTL = 15 * time.Minute

	// PairingNewPerHour is how many fresh codes one console may get in an hour. A live code is
	// reused, so a console only needs a new one every PairingTTL.
	PairingNewPerHour = 6

	// PairingMaxLive caps live codes across the network (the link-number space is 6000).
	PairingMaxLive = 2000

	pairingAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // 32 symbols, no I/O/0/1
	pairingLength   = 8

	// A link number is a NAS return code 4000-9999. Every DWC title shows a refused NAS login
	// as "Error Code: 2xxxx" (20000 + return code), on the Wii and the DS alike (step 0 of
	// prds#139: 1..9999 display as-is, five digits fall back to 20101), so the player reads
	// 24000-29999 and types that. 6000 numbers are guessable, so a claim by number needs a
	// second factor (NumberProof).
	linkNumberMin = 4000
	linkNumberMax = 9999
	// LinkNumberDisplayOffset turns a link number into the error code the console shows.
	LinkNumberDisplayOffset = 20000
)

// Unit codes, as in the NAS token.
const (
	UnitDS  = 0
	UnitWii = 1
)

var (
	// ErrPairingCode is a code that is unknown, expired, used or being claimed by another
	// account. They are one answer on purpose: telling them apart would help someone guessing.
	ErrPairingCode = errors.New("pairing code unknown, expired or used")
	// ErrPairingLimit means no new code may be issued now (per-console or global cap).
	ErrPairingLimit = errors.New("pairing code limit reached")
	// ErrConsoleLinked is an approval for a console that is already linked to another account.
	ErrConsoleLinked = errors.New("console already linked")
)

const createPairing = `
CREATE TABLE IF NOT EXISTS wfc_pairing_codes (
	code text PRIMARY KEY,
	user_id bigint NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	expires_at timestamptz NOT NULL,
	used_at timestamptz
);
CREATE INDEX IF NOT EXISTS wfc_pairing_codes_user ON wfc_pairing_codes (user_id);
ALTER TABLE wfc_pairing_codes
	ADD COLUMN IF NOT EXISTS number integer,
	ADD COLUMN IF NOT EXISTS unit smallint NOT NULL DEFAULT 1,
	ADD COLUMN IF NOT EXISTS gsbrcd text NOT NULL DEFAULT '',
	ADD COLUMN IF NOT EXISTS name text NOT NULL DEFAULT '',
	ADD COLUMN IF NOT EXISTS approving text,
	ADD COLUMN IF NOT EXISTS subject text,
	ADD COLUMN IF NOT EXISTS net_tag bytea;
CREATE UNIQUE INDEX IF NOT EXISTS wfc_pairing_codes_live_number
	ON wfc_pairing_codes (number) WHERE used_at IS NULL AND number IS NOT NULL;
CREATE TABLE IF NOT EXISTS wfc_pairing_linked (
	user_id bigint PRIMARY KEY,
	linked_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE wfc_pairing_linked
	ADD COLUMN IF NOT EXISTS subject text NOT NULL DEFAULT '',
	ADD COLUMN IF NOT EXISTS link_id text NOT NULL DEFAULT '',
	ADD COLUMN IF NOT EXISTS account_id text NOT NULL DEFAULT '',
	ADD COLUMN IF NOT EXISTS unit smallint NOT NULL DEFAULT 1;
CREATE INDEX IF NOT EXISTS wfc_pairing_linked_subject ON wfc_pairing_linked (subject);
CREATE INDEX IF NOT EXISTS wfc_pairing_linked_account ON wfc_pairing_linked (account_id);
`

// EnsurePairing creates the pairing tables.
func (c *Connection) EnsurePairing() error {
	_, err := c.pool.Exec(c.ctx, createPairing)
	if err != nil {
		logging.Error("DB", "pairing tables:", err)
	}
	return err
}

// NewPairingCode returns a random code of pairingLength symbols. The alphabet has 32
// symbols, a divisor of 256, so every byte maps evenly and there is no modulo bias.
func NewPairingCode() string {
	var raw [pairingLength]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	out := make([]byte, pairingLength)
	for i, b := range raw {
		out[i] = pairingAlphabet[int(b)%len(pairingAlphabet)]
	}
	return string(out)
}

// NewLinkNumber returns a uniform random link number in [linkNumberMin, linkNumberMax].
func NewLinkNumber() int {
	span := uint64(linkNumberMax - linkNumberMin + 1)
	limit := (^uint64(0) / span) * span // rejection sampling: no modulo bias
	var b [8]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			panic(err)
		}
		if v := binary.BigEndian.Uint64(b[:]); v < limit {
			return linkNumberMin + int(v%span)
		}
	}
}

// NormalizePairingCode upper-cases a typed code and drops spaces and dashes. ok is false when
// what is left cannot be a code.
func NormalizePairingCode(raw string) (string, bool) {
	c := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(raw)))
	if len(c) != pairingLength {
		return "", false
	}
	for _, r := range c {
		if !strings.ContainsRune(pairingAlphabet, r) {
			return "", false
		}
	}
	return c, true
}

// ParsePairingInput reads what a player typed: an 8-symbol code (code set) or the 5-digit error
// code a refused console showed (number set to the link number). ok is false when it is neither.
func ParsePairingInput(raw string) (code string, number int, ok bool) {
	if c, ok := NormalizePairingCode(raw); ok {
		return c, 0, true
	}
	d := strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(raw))
	if len(d) == 5 {
		if n, err := strconv.Atoi(d); err == nil && n-LinkNumberDisplayOffset >= linkNumberMin && n-LinkNumberDisplayOffset <= linkNumberMax {
			return "", n - LinkNumberDisplayOffset, true
		}
	}
	return "", 0, false
}

// DisplayNumber is the error code a console shows for a link number.
func DisplayNumber(n int) int { return n + LinkNumberDisplayOffset }

// FormatPairingCode writes a code as XXXX-XXXX for display.
func FormatPairingCode(code string) string {
	if len(code) != pairingLength {
		return code
	}
	return code[:4] + "-" + code[4:]
}

// SubjectFor is the account-core subject of one link of a console: "<user_id>.<n>", n the
// approval's start in Unix nanoseconds. A fresh subject per link lets a console unlinked from
// one account be linked to another (the core refuses to move a revoked subject between
// accounts), the way the Xbox mints a fresh XUID. A time stamp, not a counter, so an erased
// console that comes back can never meet one of its old, revoked subjects.
func SubjectFor(userID uint64, n int64) string {
	return strconv.FormatUint(userID, 10) + "." + strconv.FormatInt(n, 10)
}

// SubjectUserID reads the console user id from a subject: "<user_id>.<n>", or a bare
// "<user_id>" from before link-first.
func SubjectUserID(subject string) (uint64, bool) {
	if i := strings.IndexByte(subject, '.'); i >= 0 {
		subject = subject[:i]
	}
	id, err := strconv.ParseUint(subject, 10, 64)
	return id, err == nil && id != 0
}

// PairingRequest is the console asking for a code, as the gate saw it.
type PairingRequest struct {
	UserID   uint64
	Unit     byte
	GameCode string // gsbrcd of the game that asked, for the confirm screen
	Name     string // in-game name as sent, for the confirm screen
	NetTag   []byte // NetTag of the console's address, for a claim by number (never the address)
}

// Pairing is a live code and its link number.
type Pairing struct {
	Code      string
	Number    int
	ExpiresAt time.Time
}

// IssuePairingCode returns the console's live code (refreshing the game and name it shows),
// or issues a new one. ErrPairingLimit when the console asked too often or the network has too
// many live codes.
func (c *Connection) IssuePairingCode(ctx context.Context, req PairingRequest) (Pairing, error) {
	var p Pairing
	var num *int
	err := c.pool.QueryRow(ctx, `
UPDATE wfc_pairing_codes SET gsbrcd = $2, name = $3, unit = $4, net_tag = COALESCE($5, net_tag)
WHERE code = (SELECT code FROM wfc_pairing_codes
	WHERE user_id = $1 AND used_at IS NULL AND approving IS NULL AND expires_at > now()
	ORDER BY created_at DESC LIMIT 1)
RETURNING code, number, expires_at`, int64(req.UserID), req.GameCode, req.Name, int16(req.Unit), req.NetTag).Scan(&p.Code, &num, &p.ExpiresAt)
	if err == nil {
		if num != nil {
			p.Number = *num
		}
		return p, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Pairing{}, err
	}

	// Expired and used rows are dead weight; drop them as we go.
	_, _ = c.pool.Exec(ctx, `DELETE FROM wfc_pairing_codes WHERE expires_at < now() - interval '1 day' OR used_at < now() - interval '1 day'`)

	var recent, live int
	if err := c.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM wfc_pairing_codes WHERE user_id = $1 AND created_at > now() - interval '1 hour'),
		(SELECT count(*) FROM wfc_pairing_codes WHERE used_at IS NULL AND expires_at > now())`,
		int64(req.UserID)).Scan(&recent, &live); err != nil {
		return Pairing{}, err
	}
	if recent >= PairingNewPerHour || live >= PairingMaxLive {
		return Pairing{}, ErrPairingLimit
	}

	for attempt := 0; attempt < 16; attempt++ {
		code, number := NewPairingCode(), NewLinkNumber()
		tag, err := c.pool.Exec(ctx, `
INSERT INTO wfc_pairing_codes (code, user_id, expires_at, number, unit, gsbrcd, name, net_tag)
VALUES ($1, $2, now() + make_interval(secs => $3), $4, $5, $6, $7, $8) ON CONFLICT DO NOTHING`,
			code, int64(req.UserID), PairingTTL.Seconds(), number, int16(req.Unit), req.GameCode, req.Name, req.NetTag)
		if err != nil {
			var pgErr interface{ SQLState() string }
			if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" { // live number taken
				continue
			}
			return Pairing{}, err
		}
		if tag.RowsAffected() == 1 {
			if err := c.pool.QueryRow(ctx, `SELECT expires_at FROM wfc_pairing_codes WHERE code = $1`, code).Scan(&p.ExpiresAt); err != nil {
				return Pairing{}, err
			}
			return Pairing{Code: code, Number: number, ExpiresAt: p.ExpiresAt}, nil
		}
	}
	return Pairing{}, errors.New("could not find a free pairing code")
}

// PairingTarget is the console a live code belongs to.
type PairingTarget struct {
	Code      string // the canonical 8-symbol code, whatever was typed
	UserID    uint64
	Unit      byte
	ExpiresAt time.Time
	GameCode  string // the game that asked, for the confirm screen
	Name      string
	Subject   string // set once an approval has started
	ByNumber  bool   // matched by link number (a NumberProof held), not by code
}

// NumberProof is the second factor a claim by link number needs (a code needs none): the
// claimer's network tags (today's and yesterday's, from NetTags) or the console's in-game name
// as the player reads it on the console. Either one matching is enough.
type NumberProof struct {
	NetTags [][]byte
	Name    string
}

func (p NumberProof) holds(tag []byte, name string) bool {
	for _, t := range p.NetTags {
		if len(tag) > 0 && bytes.Equal(t, tag) {
			return true
		}
	}
	n := strings.TrimSpace(name)
	return n != "" && strings.EqualFold(strings.TrimSpace(p.Name), n)
}

const pairingWhere = `used_at IS NULL AND expires_at > now() AND (code = $1 OR ($2 <> 0 AND number = $2))`

func pairingArgs(raw string) (string, int, error) {
	code, number, ok := ParsePairingInput(raw)
	if !ok {
		return "", 0, ErrPairingCode
	}
	return code, number, nil
}

// LookupPairingCode reports the console behind a live code or link number without spending it.
// A code that another account is approving reads as unknown.
// A link number resolves only with a NumberProof that holds; otherwise it reads as unknown.
func (c *Connection) LookupPairingCode(ctx context.Context, raw string, proof NumberProof) (PairingTarget, error) {
	code, number, err := pairingArgs(raw)
	if err != nil {
		return PairingTarget{}, err
	}
	var t PairingTarget
	var uid int64
	var unit int16
	var tag []byte
	err = c.pool.QueryRow(ctx, `SELECT code, user_id, unit, expires_at, gsbrcd, name, net_tag FROM wfc_pairing_codes
		WHERE `+pairingWhere+` AND approving IS NULL`, code, number).Scan(&t.Code, &uid, &unit, &t.ExpiresAt, &t.GameCode, &t.Name, &tag)
	if errors.Is(err, pgx.ErrNoRows) {
		return PairingTarget{}, ErrPairingCode
	}
	if err != nil {
		return PairingTarget{}, err
	}
	if number != 0 && !proof.holds(tag, t.Name) {
		return PairingTarget{}, ErrPairingCode
	}
	t.UserID, t.Unit, t.ByNumber = uint64(uid), byte(unit), number != 0
	return t, nil
}

// BeginApproval claims a live code for an account and gives it the core subject it will be
// linked under. It is idempotent for the same account: a retry gets the same subject back.
// A code another account is approving reads as unknown. alreadyLinked is set when the console
// is linked to this same account already; ErrConsoleLinked when it is linked to another.
func (c *Connection) BeginApproval(ctx context.Context, raw, accountID string, proof NumberProof) (t PairingTarget, alreadyLinked bool, err error) {
	code, number, err := pairingArgs(raw)
	if err != nil || accountID == "" {
		return PairingTarget{}, false, ErrPairingCode
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return PairingTarget{}, false, err
	}
	defer tx.Rollback(ctx)

	var uid int64
	var unit int16
	var approving, subject *string
	var tag []byte
	err = tx.QueryRow(ctx, `SELECT code, user_id, unit, expires_at, gsbrcd, name, approving, subject, net_tag FROM wfc_pairing_codes
		WHERE `+pairingWhere+` FOR UPDATE`, code, number).Scan(&t.Code, &uid, &unit, &t.ExpiresAt, &t.GameCode, &t.Name, &approving, &subject, &tag)
	if errors.Is(err, pgx.ErrNoRows) {
		return PairingTarget{}, false, ErrPairingCode
	}
	if err != nil {
		return PairingTarget{}, false, err
	}
	if number != 0 && !proof.holds(tag, t.Name) {
		return PairingTarget{}, false, ErrPairingCode
	}
	t.ByNumber = number != 0
	if approving != nil && *approving != accountID {
		return PairingTarget{}, false, ErrPairingCode
	}
	t.UserID, t.Unit = uint64(uid), byte(unit)

	var linkedAccount string
	err = tx.QueryRow(ctx, `SELECT account_id FROM wfc_pairing_linked WHERE user_id = $1`, uid).Scan(&linkedAccount)
	switch {
	case err == nil && linkedAccount == accountID:
		return t, true, nil
	case err == nil:
		return PairingTarget{}, false, ErrConsoleLinked
	case !errors.Is(err, pgx.ErrNoRows):
		return PairingTarget{}, false, err
	}

	if subject != nil && *subject != "" {
		t.Subject = *subject
	} else {
		t.Subject = SubjectFor(t.UserID, time.Now().UnixNano())
	}
	if _, err := tx.Exec(ctx, `UPDATE wfc_pairing_codes SET approving = $2, subject = $3 WHERE code = $1`,
		t.Code, accountID, t.Subject); err != nil {
		return PairingTarget{}, false, err
	}
	return t, false, tx.Commit(ctx)
}

// FinishApproval records the console as linked under the core link and spends the code, in
// one transaction. Idempotent.
func (c *Connection) FinishApproval(ctx context.Context, t PairingTarget, accountID, linkID string) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
INSERT INTO wfc_pairing_linked (user_id, subject, link_id, account_id, unit) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id) DO UPDATE SET subject = EXCLUDED.subject, link_id = EXCLUDED.link_id,
	account_id = EXCLUDED.account_id, unit = EXCLUDED.unit, linked_at = now()`,
		int64(t.UserID), t.Subject, linkID, accountID, int16(t.Unit)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE wfc_pairing_codes SET used_at = now() WHERE code = $1 AND used_at IS NULL`, t.Code); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AbortApproval releases a code an approval gave up on. The subject is dropped with it: the
// core link under it was rolled back, and a later claim (perhaps by another account) gets a
// fresh one.
func (c *Connection) AbortApproval(ctx context.Context, code, accountID string) error {
	_, err := c.pool.Exec(ctx, `UPDATE wfc_pairing_codes SET approving = NULL, subject = NULL
		WHERE code = $1 AND approving = $2 AND used_at IS NULL`, code, accountID)
	return err
}

// StuckApproval is an approval whose code expired before it finished.
type StuckApproval struct {
	PairingTarget
	AccountID string
}

// StuckApprovals lists approvals left half-done past their code's expiry.
func (c *Connection) StuckApprovals(ctx context.Context) ([]StuckApproval, error) {
	rows, err := c.pool.Query(ctx, `SELECT code, user_id, unit, expires_at, gsbrcd, name, subject, approving
		FROM wfc_pairing_codes WHERE approving IS NOT NULL AND used_at IS NULL AND expires_at < now() - interval '1 minute'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StuckApproval
	for rows.Next() {
		var s StuckApproval
		var uid int64
		var unit int16
		var subject *string
		if err := rows.Scan(&s.Code, &uid, &unit, &s.ExpiresAt, &s.GameCode, &s.Name, &subject, &s.AccountID); err != nil {
			return nil, err
		}
		s.UserID, s.Unit = uint64(uid), byte(unit)
		if subject != nil {
			s.Subject = *subject
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// PendingApproval is the in-flight approval for a console, if any (the gate's self-heal).
func (c *Connection) PendingApproval(ctx context.Context, userID uint64) (StuckApproval, bool, error) {
	var s StuckApproval
	var uid int64
	var unit int16
	err := c.pool.QueryRow(ctx, `SELECT code, user_id, unit, expires_at, gsbrcd, name, subject, approving
		FROM wfc_pairing_codes WHERE user_id = $1 AND approving IS NOT NULL AND subject IS NOT NULL AND used_at IS NULL
		ORDER BY created_at DESC LIMIT 1`, int64(userID)).Scan(&s.Code, &uid, &unit, &s.ExpiresAt, &s.GameCode, &s.Name, &s.Subject, &s.AccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return StuckApproval{}, false, nil
	}
	if err != nil {
		return StuckApproval{}, false, err
	}
	s.UserID, s.Unit = uint64(uid), byte(unit)
	return s, true, nil
}

// LinkedConsole is a console's cached core link.
type LinkedConsole struct {
	UserID    uint64
	Subject   string
	LinkID    string
	AccountID string
	Unit      byte
	LinkedAt  time.Time
	Games     []string // gsbrcd of the games it has played online
}

const linkedCols = `l.user_id, l.subject, l.link_id, l.account_id, l.unit, l.linked_at,
	COALESCE((SELECT array_agg(DISTINCT u.gsbrcd) FROM users u WHERE u.user_id = l.user_id), '{}')`

func scanLinked(row pgx.Row) (LinkedConsole, error) {
	var l LinkedConsole
	var uid int64
	var unit int16
	err := row.Scan(&uid, &l.Subject, &l.LinkID, &l.AccountID, &unit, &l.LinkedAt, &l.Games)
	l.UserID, l.Unit = uint64(uid), byte(unit)
	return l, err
}

// LinkedConsoleOf returns the console's cached link; ok is false when it is not linked.
func (c *Connection) LinkedConsoleOf(ctx context.Context, userID uint64) (LinkedConsole, bool, error) {
	l, err := scanLinked(c.pool.QueryRow(ctx, `SELECT `+linkedCols+` FROM wfc_pairing_linked l WHERE l.user_id = $1`, int64(userID)))
	if errors.Is(err, pgx.ErrNoRows) {
		return LinkedConsole{}, false, nil
	}
	return l, err == nil, err
}

// ConsolesOf lists an account's linked consoles, oldest first.
func (c *Connection) ConsolesOf(ctx context.Context, accountID string) ([]LinkedConsole, error) {
	rows, err := c.pool.Query(ctx, `SELECT `+linkedCols+` FROM wfc_pairing_linked l WHERE l.account_id = $1 ORDER BY l.linked_at`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LinkedConsole
	for rows.Next() {
		l, err := scanLinked(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ForgetPairing marks a console unlinked again, so its next login is refused with a code.
func (c *Connection) ForgetPairing(ctx context.Context, userID uint64) error {
	_, err := c.pool.Exec(ctx, `DELETE FROM wfc_pairing_linked WHERE user_id = $1`, int64(userID))
	forgetLinkedCache()
	return err
}

// ForgetSubject unlinks the console cached under a core subject; it returns the console's
// user id (0 when no console had that subject).
func (c *Connection) ForgetSubject(ctx context.Context, subject string) (uint64, error) {
	var uid int64
	err := c.pool.QueryRow(ctx, `DELETE FROM wfc_pairing_linked WHERE subject = $1 RETURNING user_id`, subject).Scan(&uid)
	forgetLinkedCache()
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return uint64(uid), err
}

// ForgetAccount unlinks every console of an account; it returns their user ids.
func (c *Connection) ForgetAccount(ctx context.Context, accountID string) ([]uint64, error) {
	if accountID == "" {
		return nil, nil
	}
	rows, err := c.pool.Query(ctx, `DELETE FROM wfc_pairing_linked WHERE account_id = $1 RETURNING user_id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uint64
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out = append(out, uint64(uid))
	}
	forgetLinkedCache()
	return out, rows.Err()
}

// PairingStats are counters for /api/stats.
type PairingStats struct {
	LiveCodes     int `json:"pair_codes_live"`
	Issued24h     int `json:"pair_issued_24h"`
	Approved24h   int `json:"pair_approved_24h"`
	LinkedConsole int `json:"linked_consoles"`
}

// PairingCounters reads the pairing counters.
func (c *Connection) PairingCounters(ctx context.Context) (PairingStats, error) {
	var s PairingStats
	err := c.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM wfc_pairing_codes WHERE used_at IS NULL AND expires_at > now()),
		(SELECT count(*) FROM wfc_pairing_codes WHERE created_at > now() - interval '1 day'),
		(SELECT count(*) FROM wfc_pairing_codes WHERE used_at > now() - interval '1 day'),
		(SELECT count(*) FROM wfc_pairing_linked)`).Scan(&s.LiveCodes, &s.Issued24h, &s.Approved24h, &s.LinkedConsole)
	return s, err
}

// codeHashKey salts CodeHash per process, so a logged tag cannot be matched against codes.
var codeHashKey = func() []byte {
	k := make([]byte, 16)
	if _, err := rand.Read(k); err != nil {
		panic(err)
	}
	return k
}()

// CodeHash is a short, salted, non-reversible tag for a code, for log lines (codes are never
// logged). The same code gives the same tag within one process run.
func CodeHash(code string) string {
	m := hmac.New(sha256.New, codeHashKey)
	m.Write([]byte(code))
	return fmt.Sprintf("%x", m.Sum(nil)[:3])
}

// NetTagKey is the key network tags are made with: WFC_NET_TAG_KEY, else WFC_INTERNAL_KEY.
// wfc-nas and wfc-api must agree on it; nobody else needs it.
func NetTagKey() []byte {
	if k := os.Getenv("WFC_NET_TAG_KEY"); k != "" {
		return []byte(k)
	}
	return []byte(os.Getenv("WFC_INTERNAL_KEY"))
}

// NetTag is a keyed, day-scoped hash of the network an address belongs to (the IPv4 address,
// or the IPv6 /64), so a console's network and a browser's can be compared without keeping
// either address. nil for an unusable address or an empty key.
func NetTag(key []byte, ip string, day time.Time) []byte {
	if len(key) == 0 {
		return nil
	}
	if h, _, err := net.SplitHostPort(ip); err == nil {
		ip = h
	}
	a := net.ParseIP(strings.Trim(ip, "[]"))
	if a == nil {
		return nil
	}
	prefix := ""
	if v4 := a.To4(); v4 != nil {
		prefix = v4.String()
	} else {
		prefix = a.Mask(net.CIDRMask(64, 128)).String() + "/64"
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte("wfc-nettag|" + day.UTC().Format("2006-01-02") + "|" + prefix))
	return m.Sum(nil)[:16]
}

// NetTags are an address's tags for today and yesterday (a code can outlive midnight).
func NetTags(key []byte, ip string, now time.Time) [][]byte {
	var out [][]byte
	for _, d := range []time.Time{now, now.Add(-24 * time.Hour)} {
		if t := NetTag(key, ip, d); t != nil {
			out = append(out, t)
		}
	}
	return out
}
