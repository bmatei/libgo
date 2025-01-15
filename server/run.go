package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
)

func RunServer(cfg *Config, handler http.Handler) {
	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.ListenAddress, cfg.Port),
		Handler: handler,
	}

	log.Info().Str("addr", srv.Addr).Msg("Listening")

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Error().Err(err).Msg("HTTP listen and serve")
		}
	}()

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT)
	<-signalChan

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("Shutdown error")
		defer os.Exit(1)

		return
	} else {
		log.Info().Msg("gracefully stopped")
	}

}
