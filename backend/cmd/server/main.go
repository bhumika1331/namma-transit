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
	"github.com/bhumika1331/namma-transit/backend/internal/store/sqlite"
	"github.com/bhumika1331/namma-transit/backend/internal/timeprovider"
)

// version is overridden at build time: -ldflags "-X main.version=..."
var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	port := envOr("PORT", "8080")
	// DATA_SOURCE selects where the network comes from: "gtfs" reads the
	// community zips in GTFS_DIR; "sqlite" reads DB_PATH filled by loadgtfs
	// or the scraper.
	source := envOr("DATA_SOURCE", "gtfs")
	gtfsDir := envOr("GTFS_DIR", "data/gtfs")
	dbPath := envOr("DB_PATH", "data/transit.db")
	var origins []string
	if v := os.Getenv("ALLOWED_ORIGINS"); v != "" {
		origins = strings.Split(v, ",")
	}

	var net *network.Network
	var err error
	switch source {
	case "sqlite":
		net, err = loadFromSQLite(log, dbPath)
	default:
		net, err = loadNetwork(log, gtfsDir)
	}
	if err != nil {
		log.Error("load network", "err", err)
		os.Exit(1)
	}
	tables, err := fare.LoadEmbedded()
	if err != nil {
		log.Error("load fares", "err", err)
		os.Exit(1)
	}
	// Geocoder chain: Ola Maps when a key is configured (best Indian
	// coverage), then keyless Photon (OpenStreetMap) as fallback.
	var geo places.Chain
	if key := os.Getenv("OLA_MAPS_API_KEY"); key != "" {
		geo = append(geo, places.NewOlaMaps(key))
	}
	geo = append(geo, places.NewPhoton("namma-transit/"+version+" (personal project; github.com/bhumika1331/namma-transit)"))
	idx := places.New(net, geo)
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

// loadFromSQLite builds the network from every dataset in the store.
func loadFromSQLite(log *slog.Logger, path string) (*network.Network, error) {
	st, err := sqlite.Open(path, true)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	ctx := context.Background()
	names, err := st.Datasets(ctx)
	if err != nil {
		return nil, err
	}
	var sources []network.Source
	for _, name := range names {
		start := time.Now()
		ds, err := st.LoadDataset(ctx, name)
		if err != nil {
			return nil, err
		}
		log.Info("loaded dataset", "name", name, "source", ds.Source(), "version", ds.Version(), "stops", len(ds.Stops()), "routes", len(ds.Routes()), "took", time.Since(start).Round(time.Millisecond))
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
