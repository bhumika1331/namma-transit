// Command server runs the namma-transit Connect API.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bhumika1331/namma-transit/backend/internal/api"
	"github.com/bhumika1331/namma-transit/backend/internal/fare"
	"github.com/bhumika1331/namma-transit/backend/internal/gtfs"
	"github.com/bhumika1331/namma-transit/backend/internal/network"
	"github.com/bhumika1331/namma-transit/backend/internal/places"
	"github.com/bhumika1331/namma-transit/backend/internal/planner"
	"github.com/bhumika1331/namma-transit/backend/internal/timeprovider"
)

// version is overridden at build time: -ldflags "-X main.version=..."
var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	port := envOr("PORT", "8080")
	gtfsDir := envOr("GTFS_DIR", "data/gtfs")
	var origins []string
	if v := os.Getenv("ALLOWED_ORIGINS"); v != "" {
		origins = strings.Split(v, ",")
	}

	net, err := loadNetwork(log, gtfsDir)
	if err != nil {
		log.Error("load network", "err", err)
		os.Exit(1)
	}
	tables, err := fare.LoadEmbedded()
	if err != nil {
		log.Error("load fares", "err", err)
		os.Exit(1)
	}
	idx := places.New(net, nil)
	pl := planner.New(net, timeprovider.NewSchedule(), &fare.Engine{Tables: tables}, idx)

	handler := api.NewHandler(api.Options{
		Meta:           api.NewMetaServer(version, api.NetworkStatus{Net: net, Fares: tables}),
		Place:          &api.PlaceServer{Index: idx},
		Trip:           &api.TripServer{Planner: pl},
		AllowedOrigins: origins,
	})
	srv := &http.Server{Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Info("listening", "addr", srv.Addr, "version", version, "data", net.Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "err", err)
	}
}

// loadNetwork loads whichever community feeds are present in gtfsDir.
func loadNetwork(log *slog.Logger, dir string) (*network.Network, error) {
	var sources []network.Source
	for _, name := range []string{"bmrcl", "bmtc"} {
		path := filepath.Join(dir, name+".zip")
		if _, err := os.Stat(path); err != nil {
			log.Warn("feed missing, skipping", "path", path)
			continue
		}
		start := time.Now()
		ds, err := gtfs.Load(path, gtfs.Options{Name: name})
		if err != nil {
			return nil, err
		}
		log.Info("loaded feed", "name", name, "version", ds.Version(), "stops", len(ds.Stops()), "routes", len(ds.Routes()), "took", time.Since(start).Round(time.Millisecond))
		sources = append(sources, ds)
	}
	return network.Build(sources, network.BuildOptions{})
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
