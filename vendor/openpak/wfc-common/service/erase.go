package service

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
)

var erasePath = regexp.MustCompile(`^/internal/accounts/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})/data$`)

// EraseHandler serves DELETE /internal/accounts/{account_id}/data (card openpak/prds#120).
// The shared bearer key comes from ACCOUNT_ERASE_KEY; when it is unset the endpoint answers
// 503 and never runs. erase resolves and deletes the account's data and returns the row count.
func EraseHandler(serviceName string, erase func(ctx context.Context, accountID string) (int, error)) http.Handler {
	return eraseHandler(serviceName, os.Getenv("ACCOUNT_ERASE_KEY"), erase)
}

func eraseHandler(serviceName, key string, erase func(ctx context.Context, accountID string) (int, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := erasePath.FindStringSubmatch(r.URL.Path)
		if m == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if key == "" {
			http.Error(w, "erase not configured", http.StatusServiceUnavailable)
			return
		}
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(tok), []byte(key)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		account := strings.ToLower(m[1])
		rows, err := erase(r.Context(), account)
		if err != nil {
			slog.Error("account erase failed", "service", serviceName, "account", account)
			http.Error(w, "erase failed", http.StatusInternalServerError)
			return
		}
		slog.Info("account erased", "account", account, "rows", rows, "service", serviceName)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"service": serviceName, "rows": rows})
	})
}
