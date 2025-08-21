package main

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"

	"github.com/rkitindi-kr/pvc-webhook/internal/webhook"
)

func main() {
	zl, _ := zap.NewProduction()
	log := zapr.NewLogger(zl)

	addr := ":9443"
	if v := os.Getenv("WEBHOOK_ADDR"); v != "" {
		addr = v
	}

	h := webhook.NewHandler(log)

	mux := http.NewServeMux()
	mux.Handle("/mutate", h)

	srv := &http.Server{
		Addr:      addr,
		Handler:   mux,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}

	certFile := "/tls/tls.crt"
	keyFile := "/tls/tls.key"
	if _, err := os.Stat(certFile); err != nil {
		log.Error(err, "missing certFile")
		os.Exit(1)
	}
	log.Info("starting webhook server", "addr", addr)
	if err := srv.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
		log.Error(err, "webhook server failed")
		os.Exit(1)
	}
}

