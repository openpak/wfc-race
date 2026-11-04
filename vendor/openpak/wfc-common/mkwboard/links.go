package mkwboard

import (
	"context"
	"strconv"
	"sync"
	"time"

	accountv1 "github.com/openpak/account-api/openpak/account/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// CoreLinks resolves a console user id to an account through the account core's Links service
// (namespace "wfc"). Positive answers are cached for a minute; a missing link is asked again.
type CoreLinks struct {
	cli accountv1.LinksClient
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
	return &CoreLinks{cli: accountv1.NewLinksClient(conn), key: key, cache: map[int64]cached{}}, conn, nil
}

// Account implements Linker.
func (l *CoreLinks) Account(ctx context.Context, userID int64) (string, bool, error) {
	l.mu.Lock()
	c, ok := l.cache[userID]
	l.mu.Unlock()
	if ok && time.Now().Before(c.until) {
		return c.account, true, nil
	}
	ctx, cancel := context.WithTimeout(metadata.AppendToOutgoingContext(ctx, "X-API-Key", l.key), callLimit)
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
