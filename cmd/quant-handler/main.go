package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"

	elog "github.com/hushine-tech/golang-lib/pkg/log"
	"github.com/hushine-tech/quant-handler/internal/app"
	"github.com/hushine-tech/quant-handler/internal/config"
	"github.com/hushine-tech/quant-handler/internal/logger"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config.yaml")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("config file %q not found; using built-in defaults + env overrides", *configPath)
			cfg = config.Default()
		} else {
			log.Fatalf("load config: %v", err)
		}
	}
	cfg.ApplyEnvOverrides()

	if err := logger.InitWithConfig(&cfg.Log); err != nil {
		log.Fatalf("init logger: %v", err)
	}
	defer logger.Close()

	if cfg.Log.Tracing.Enabled {
		if tracerShutdown, err := elog.InitTracerFromConfig(cfg.Log.Tracing); err != nil {
			log.Printf("init tracer: %v (continuing without tracing)", err)
		} else {
			defer tracerShutdown(context.Background())
		}
	}

	if err := app.Run(cfg); err != nil {
		log.Printf("quant-handler: %v", err)
		os.Exit(1)
	}
}
