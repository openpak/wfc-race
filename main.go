package main

import (
	"context"
	"net/http"
	"openpak/wfc-common/common"
	"openpak/wfc-common/logging"
	"openpak/wfc-common/service"
	"openpak/wfc-race/race"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if e := common.LoadTokenKey(); e != nil {
		panic(e)
	}
	cfg := common.GetConfig()
	logging.SetLevel(*cfg.LogLevel)
	cfg.RegisterWebhooks()
	common.ReadGameList()
	os.MkdirAll("state", 0700)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	race.StartServer(false)
	defer race.Shutdown()
	mux := http.NewServeMux()
	race.RegisterHandlers(mux)
	h := http.NewServeMux()
	h.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	h.Handle("/", service.Guard(mux))
	addr := os.Getenv("WFC_HTTP_ADDR")
	if addr == "" {
		addr = ":20208"
	}
	if e := service.Run(ctx, service.Server(addr, h)); e != nil {
		panic(e)
	}
}
