// Command loadgtfs imports community GTFS feeds into the SQLite store so the
// server can run from the same tables the scraper fills.
//
//	go run ./cmd/loadgtfs -db data/transit.db -gtfs data/gtfs
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/bhumika1331/namma-transit/backend/internal/gtfs"
	"github.com/bhumika1331/namma-transit/backend/internal/store/sqlite"
)

func main() {
	dbPath := flag.String("db", "data/transit.db", "SQLite file to write")
	dir := flag.String("gtfs", "data/gtfs", "directory holding bmrcl.zip and bmtc.zip")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	st, err := sqlite.Open(*dbPath, false)
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer st.Close()
	ctx := context.Background()
	for _, name := range []string{"bmrcl", "bmtc"} {
		path := filepath.Join(*dir, name+".zip")
		if _, err := os.Stat(path); err != nil {
			log.Warn("feed missing, skipping", "path", path)
			continue
		}
		start := time.Now()
		ds, err := gtfs.Load(path, gtfs.Options{Name: name})
		if err != nil {
			log.Error("load", "feed", name, "err", err)
			os.Exit(1)
		}
		if err := st.ReplaceDataset(ctx, ds); err != nil {
			log.Error("write", "feed", name, "err", err)
			os.Exit(1)
		}
		log.Info("imported", "feed", name, "version", ds.Version(), "stops", len(ds.Stops()), "routes", len(ds.Routes()), "took", time.Since(start).Round(time.Millisecond))
	}
	if err := st.SetMeta(ctx, "imported_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		log.Error("meta", "err", err)
		os.Exit(1)
	}
}
