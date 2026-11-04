// Package mkwboard is the Wii/DS family's adapter over the neutral leaderboards core for Mario
// Kart Wii time trials. The core owns the times and the ranking; this package only maps the
// game's identities (profile id, console user id, region, course) onto core boards:
//
//   - the console user id is linked to an account in the account core under namespace "wfc";
//   - the board is the course id (worldwide) or "r<region>-<course>" (one region), title key
//     TitleKey mapped from the GameSpy game id 1687 on network "wfc";
//   - the profile id, console user id and region ride in the entry columns, the Mii player info
//     in the entry info, and aggregation is "latest" because an upload always replaces the
//     player's previous time (the game decides what is worth uploading).
//
// A player whose console is not linked to an account has no entry in the core; Reconcile imports
// those times once the link exists.
package mkwboard

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	pb "openpak/wfc-common/mkwboard/leaderboardspb"
)

const (
	// Network is the account-core link namespace and the leaderboards network name.
	Network = "wfc"
	// GameID is the GameSpy game id of Mario Kart Wii; it is the title as the network knows it.
	GameID = "1687"
	// TitleKey is the neutral title key (the website's /t/<title> path).
	TitleKey = "mario-kart-wii"
	// TitleName is its display name.
	TitleName = "Mario Kart Wii"
	// MaxCourse is the highest course id the game accepts (competitions are above 31).
	MaxCourse = 32767
	// Courses is the number of regular courses (0-31).
	Courses = 32
	// Regions are 1-7; 0 is worldwide.
	Regions = 7

	maxRecv   = 16 << 20
	callLimit = 10 * time.Second
	infoMax   = 4096
)

// ErrNoLink is returned by Submit when the console is not linked to an account.
var ErrNoLink = errors.New("mkwboard: console is not linked to an account")

// Linker maps a console user id to the account core's account id.
type Linker interface {
	Account(ctx context.Context, userID int64) (account string, ok bool, err error)
}

// Board is the Mario Kart Wii view of the leaderboards core.
type Board struct {
	LB    pb.LeaderboardsClient
	Links Linker
	// CourseName names a course for the board (optional).
	CourseName func(course int) string

	declared sync.Once
}

// Dial connects to the leaderboards gRPC service and sends the internal key on every call.
func Dial(addr, key string, extra ...grpc.DialOption) (pb.LeaderboardsClient, *grpc.ClientConn, error) {
	opts := append([]grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxRecv)),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, inv grpc.UnaryInvoker, o ...grpc.CallOption) error {
			return inv(metadata.AppendToOutgoingContext(ctx, "x-internal-key", key), method, req, reply, cc, o...)
		}),
	}, extra...)
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, nil, err
	}
	return pb.NewLeaderboardsClient(conn), conn, nil
}

// BoardID is the core board id of a course in a region (0 = worldwide).
func BoardID(region, course int) string {
	if region == 0 {
		return strconv.Itoa(course)
	}
	return fmt.Sprintf("r%d-%d", region, course)
}

func ref(region, course int) *pb.BoardRef {
	return &pb.BoardRef{Network: Network, Title: GameID, Board: BoardID(region, course)}
}

func (b *Board) policy(course int) *pb.Policy {
	name := fmt.Sprintf("Course %d", course)
	if b.CourseName != nil {
		if n := b.CourseName(course); n != "" {
			name = n
		}
	}
	return &pb.Policy{Name: name, Kind: "time", Order: "asc", Aggregation: "latest", Unit: "ms",
		CrossNetwork: pb.Tristate_TRISTATE_TRUE}
}

func ctx10(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, callLimit)
}

// Declare maps the title and declares the 32 worldwide course boards. It is safe to call from
// every process (the core upserts); Submit carries the policy as well, so a failure here only
// delays names.
func (b *Board) Declare(ctx context.Context) error {
	c, cancel := ctx10(ctx)
	_, err := b.LB.MapTitle(c, &pb.MapTitleRequest{Title: TitleKey, Name: TitleName, Network: Network, NativeId: GameID})
	cancel()
	if err != nil {
		return err
	}
	for course := 0; course < Courses; course++ {
		c, cancel := ctx10(ctx)
		_, err := b.LB.UpsertBoard(c, &pb.UpsertBoardRequest{Ref: ref(0, course), Policy: b.policy(course), TitleName: TitleName})
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

// Time is one accepted Mario Kart Wii time.
type Time struct {
	UserID     int64 // console user id (users.user_id)
	PID        int64 // GameSpy profile id
	Region     int
	Course     int
	Score      int // milliseconds
	PlayerInfo string
	At         time.Time // when the table row was written; zero when unknown
}

func (t Time) columns() map[string]int64 {
	return map[string]int64{"pid": t.PID, "uid": t.UserID, "region": int64(t.Region)}
}

// Submit files the time on the worldwide board and, for a regional upload, on the region's board.
// It returns ErrNoLink when the console has no account yet.
func (b *Board) Submit(ctx context.Context, t Time) error {
	acct, ok, err := b.Links.Account(ctx, t.UserID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoLink
	}
	return b.submit(ctx, acct, t)
}

func (b *Board) submit(ctx context.Context, acct string, t Time) error {
	if len(t.PlayerInfo) > infoMax {
		return errors.New("mkwboard: player info too large")
	}
	regions := []int{0}
	if t.Region != 0 {
		regions = append(regions, t.Region)
	}
	for _, r := range regions {
		c, cancel := ctx10(ctx)
		_, err := b.LB.Submit(c, &pb.SubmitRequest{Ref: ref(r, t.Course), Account: acct, Score: int64(t.Score),
			Columns: t.columns(), Info: []byte(t.PlayerInfo), CreatePolicy: b.policy(t.Course)})
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

// Row is one ranked entry.
type Row struct {
	Position   int // unique 1-based place
	Rank       int // competition rank (ties share it)
	Account    string
	PID        int64
	UserID     int64
	Region     int
	Score      int
	PlayerInfo string
	Updated    time.Time
}

func row(e *pb.Entry) Row {
	return Row{Position: int(e.Position), Rank: int(e.Rank), Account: e.Account, PID: e.Columns["pid"],
		UserID: e.Columns["uid"], Region: int(e.Columns["region"]), Score: int(e.Score),
		PlayerInfo: string(e.Info), Updated: time.UnixMilli(e.UpdatedUnixMs).UTC()}
}

func rows(es []*pb.Entry) []Row {
	out := make([]Row, 0, len(es))
	for _, e := range es {
		out = append(out, row(e))
	}
	return out
}

// Range lists a board by place (offset 0 is first) with the board's total.
func (b *Board) Range(ctx context.Context, region, course, offset, limit int) ([]Row, int, error) {
	c, cancel := ctx10(ctx)
	defer cancel()
	res, err := b.LB.Range(c, &pb.RangeRequest{Ref: ref(region, course), Start: int64(offset) + 1, Limit: int64(limit)})
	if err != nil {
		return nil, 0, err
	}
	return rows(res.Entries), int(res.Total), nil
}

// OfAccounts is the entries those accounts hold on a board, best first.
func (b *Board) OfAccounts(ctx context.Context, region, course int, accounts []string) ([]Row, int, error) {
	if len(accounts) == 0 {
		return nil, 0, nil
	}
	c, cancel := ctx10(ctx)
	defer cancel()
	res, err := b.LB.ByAccounts(c, &pb.ByAccountsRequest{Ref: ref(region, course), Accounts: accounts})
	if err != nil {
		return nil, 0, err
	}
	return rows(res.Entries), int(res.Total), nil
}

// Total is the number of entries on a board.
func (b *Board) Total(ctx context.Context, region, course int) (int, error) {
	c, cancel := ctx10(ctx)
	defer cancel()
	res, err := b.LB.Total(c, &pb.TotalRequest{Ref: ref(region, course)})
	if err != nil {
		return 0, err
	}
	return int(res.Total), nil
}

// Contests lists the competition course ids (above 31) that have a board.
func (b *Board) Contests(ctx context.Context) ([]int, error) {
	c, cancel := ctx10(ctx)
	defer cancel()
	res, err := b.LB.ListBoards(c, &pb.ListBoardsRequest{Network: Network, Title: GameID})
	if err != nil {
		return nil, err
	}
	var out []int
	for _, bd := range res.Boards {
		if n, err := strconv.Atoi(bd.Id); err == nil && n >= Courses {
			out = append(out, n)
		}
	}
	return out, nil
}

// Source lists the times the game's own table holds, for Reconcile.
type Source interface {
	AllTimes(ctx context.Context) ([]Time, error)
}

// Stats is what a Reconcile run did.
type Stats struct{ Seen, Imported, Present, Unlinked, Failed int }

// Reconcile imports every time the source holds that the core does not have yet, for consoles
// that are linked. A player the core already ranks is left alone unless the table row is a later,
// different upload (the core is normally the newer side), so the run is idempotent and safe to repeat; the source is never modified.
func (b *Board) Reconcile(ctx context.Context, src Source) (Stats, error) {
	var st Stats
	times, err := src.AllTimes(ctx)
	if err != nil {
		return st, err
	}
	for _, t := range times {
		st.Seen++
		acct, ok, err := b.Links.Account(ctx, t.UserID)
		if err != nil {
			st.Failed++
			continue
		}
		if !ok {
			st.Unlinked++
			continue
		}
		have, _, err := b.OfAccounts(ctx, 0, t.Course, []string{acct})
		if err != nil {
			st.Failed++
			continue
		}
		// The core wins unless the table row is a different, later upload (a submit that
		// failed while the core was down).
		if len(have) > 0 && !(have[0].Score != t.Score && t.At.After(have[0].Updated)) {
			st.Present++
			continue
		}
		if err := b.submit(ctx, acct, t); err != nil {
			st.Failed++
			continue
		}
		st.Imported++
	}
	return st, nil
}
