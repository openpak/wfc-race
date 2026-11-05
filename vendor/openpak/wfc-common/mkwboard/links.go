package mkwboard

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	accountv1 "github.com/openpak/account-api/openpak/account/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"openpak/wfc-common/database"
)

// CoreLinks resolves a console user id to an account through the account core's Links service
// (namespace "wfc"). Positive answers are cached for a minute; a missing link is asked again.
type CoreLinks struct {
	cli accountv1.LinksClient
	ids accountv1.IdentityClient
	key string

	mu    sync.Mutex
	cache map[int64]cached
}

type cached struct {
	account string
	until   time.Time
}

// DialCore connects to the account core at addr with its API key.
func DialCore(addr, key string, extra ...grpc.DialOption) (*CoreLinks, *grpc.ClientConn, error) {
	opts := append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, extra...)
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, nil, err
	}
	return &CoreLinks{cli: accountv1.NewLinksClient(conn), ids: accountv1.NewIdentityClient(conn), key: key, cache: map[int64]cached{}}, conn, nil
}

// Account implements Linker.
func (l *CoreLinks) Account(ctx context.Context, userID int64) (string, bool, error) {
	l.mu.Lock()
	c, ok := l.cache[userID]
	l.mu.Unlock()
	if ok && time.Now().Before(c.until) {
		return c.account, true, nil
	}
	ctx, cancel := context.WithTimeout(metadata.AppendToOutgoingContext(ctx, "X-API-Key", l.key, "x-internal-key", l.key), callLimit)
	defer cancel()
	res, err := l.cli.GetLinkBySubject(ctx, &accountv1.GetLinkBySubjectRequest{Namespace: Network, SubjectId: strconv.FormatInt(userID, 10)})
	if status.Code(err) == codes.NotFound {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if res.State != accountv1.LinkState_LINK_STATE_ACTIVE || res.AccountId == "" {
		return "", false, nil
	}
	l.mu.Lock()
	l.cache[userID] = cached{res.AccountId, time.Now().Add(time.Minute)}
	l.mu.Unlock()
	return res.AccountId, true, nil
}

// UserIDs lists the console user ids (namespace "wfc") linked to an account. It is used by
// the erase path, which runs before the core drops the links.
func (l *CoreLinks) UserIDs(ctx context.Context, accountID string) ([]int64, error) {
	ctx, cancel := context.WithTimeout(metadata.AppendToOutgoingContext(ctx, "X-API-Key", l.key, "x-internal-key", l.key), callLimit)
	defer cancel()
	res, err := l.cli.ListAccountLinks(ctx, &accountv1.ListAccountLinksRequest{AccountId: accountID, Namespace: Network})
	if err != nil {
		return nil, err
	}
	var out []int64
	for _, k := range res.GetLinks() {
		if id, ok := database.SubjectUserID(k.GetSubjectId()); ok {
			out = append(out, int64(id))
		}
	}
	return out, nil
}

func (l *CoreLinks) call(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(metadata.AppendToOutgoingContext(ctx, "X-API-Key", l.key, "x-internal-key", l.key), callLimit)
}

// LinkActive implements database.CoreChecker: the active link for a subject, if any.
func (l *CoreLinks) LinkActive(ctx context.Context, subject string) (linkID, accountID string, active bool, err error) {
	ctx, cancel := l.call(ctx)
	defer cancel()
	res, err := l.cli.GetLinkBySubject(ctx, &accountv1.GetLinkBySubjectRequest{Namespace: Network, SubjectId: subject})
	if status.Code(err) == codes.NotFound {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	if res.State != accountv1.LinkState_LINK_STATE_ACTIVE || res.AccountId == "" {
		return "", "", false, nil
	}
	return res.LinkId, res.AccountId, true, nil
}

// ErrLinkConflict is the core refusing a reservation: the subject is claimed by another
// account, or the account cannot hold links.
var ErrLinkConflict = errors.New("core link conflict")

// Reserve reserves a subject for an account (idempotent by key) and returns the link id.
func (l *CoreLinks) Reserve(ctx context.Context, accountID, subject, key string) (string, error) {
	ctx, cancel := l.call(ctx)
	defer cancel()
	res, err := l.cli.ReserveLink(ctx, &accountv1.ReserveLinkRequest{AccountId: accountID, Namespace: Network, SubjectId: subject, IdempotencyKey: key})
	switch status.Code(err) {
	case codes.OK:
		return res.GetLinkId(), nil
	case codes.AlreadyExists, codes.FailedPrecondition, codes.NotFound, codes.PermissionDenied, codes.Aborted:
		return "", fmt.Errorf("%w: %v", ErrLinkConflict, err)
	}
	return "", err
}

// Activate makes a reserved link active (idempotent by key).
func (l *CoreLinks) Activate(ctx context.Context, linkID, key string) error {
	ctx, cancel := l.call(ctx)
	defer cancel()
	_, err := l.cli.ActivateLink(ctx, &accountv1.ActivateLinkRequest{LinkId: linkID, IdempotencyKey: key})
	if c := status.Code(err); c == codes.FailedPrecondition || c == codes.NotFound || c == codes.Aborted {
		return fmt.Errorf("%w: %v", ErrLinkConflict, err)
	}
	return err
}

// Unlink revokes a link (idempotent by key). The core emits link_unlinked.
func (l *CoreLinks) Unlink(ctx context.Context, linkID, key string) error {
	ctx, cancel := l.call(ctx)
	defer cancel()
	_, err := l.cli.UnlinkLink(ctx, &accountv1.UnlinkLinkRequest{LinkId: linkID, IdempotencyKey: key})
	if status.Code(err) == codes.NotFound {
		return nil
	}
	return err
}

// CachedLinker answers Account from the family's own link cache (wfc_pairing_linked, written
// when a console is linked) and asks the core only for consoles linked before link-first, whose
// subject was the bare user id.
type CachedLinker struct {
	DB   *database.Connection
	Core *CoreLinks
}

// Account implements Linker.
func (c CachedLinker) Account(ctx context.Context, userID int64) (string, bool, error) {
	if c.DB != nil {
		l, ok, err := c.DB.LinkedConsoleOf(ctx, uint64(userID))
		if err != nil {
			return "", false, err
		}
		if ok && l.AccountID != "" {
			return l.AccountID, true, nil
		}
	}
	if c.Core == nil {
		return "", false, nil
	}
	return c.Core.Account(ctx, userID)
}

// Handle is the account's public handle ("" when it has none or the account is gone).
func (l *CoreLinks) Handle(ctx context.Context, accountID string) (string, error) {
	if l.ids == nil || accountID == "" {
		return "", nil
	}
	ctx, cancel := l.call(ctx)
	defer cancel()
	res, err := l.ids.GetAccount(ctx, &accountv1.GetAccountRequest{AccountId: accountID})
	if status.Code(err) == codes.NotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return res.GetHandle(), nil
}
