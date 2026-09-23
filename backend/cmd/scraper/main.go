// Command scraper fills the SQLite store from the Namma BMTC API.
//
//	go run ./cmd/scraper -db data/transit.db -job core
//	go run ./cmd/scraper -db data/transit.db -job fares -shard 0/4
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/bhumika1331/namma-transit/backend/internal/scraper/bmtc"
	"github.com/bhumika1331/namma-transit/backend/internal/store/sqlite"
)

func main() {
	dbPath := flag.String("db", "data/transit.db", "SQLite file")
	job := flag.String("job", "core", "core | fares | all")
	base := flag.String("base", envOr("BMTC_BASE_URL", bmtc.DefaultBaseURL), "API base URL")
	rps := flag.Float64("rps", 2, "requests per second")
	only := flag.String("only", "", "comma-separated route number prefixes to limit the core job (testing)")
	shard := flag.String("shard", "0/1", "fares: index/count to split routes across runs")
	resume := flag.Bool("resume", true, "fares: continue from the stored cursor")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := sqlite.Open(*dbPath, false)
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	client := bmtc.NewClient(*base, *rps, st)
	sc := &bmtc.Scraper{Client: client, Log: log}
	if *only != "" {
		sc.Only = strings.Split(*only, ",")
	}

	if *job == "core" || *job == "all" {
		if err := runCore(ctx, log, st, sc); err != nil {
			log.Error("core job", "err", err)
			os.Exit(1)
		}
	}
	if *job == "fares" || *job == "all" {
		var sh bmtc.Shard
		if _, err := fmtSscanf(*shard, &sh.Index, &sh.Count); err != nil {
			log.Error("bad -shard", "value", *shard)
			os.Exit(2)
		}
		if err := runFares(ctx, log, st, sc, sh, *resume); err != nil {
			log.Error("fares job", "err", err)
			os.Exit(1)
		}
	}
}

func runCore(ctx context.Context, log *slog.Logger, st *sqlite.Store, sc *bmtc.Scraper) error {
	runID, err := st.StartRun(ctx, "core")
	if err != nil {
		return err
	}
	ds, prog, err := sc.ScrapeCore(ctx)
	if err != nil {
		_ = st.FinishRun(ctx, runID, "failed", sc.Client.Calls, prog.Errors, append(prog.Notes, err.Error()))
		return err
	}
	status := "ok"
	if prog.Partial {
		status = "partial"
		log.Warn("core run partial; keeping previous tables", "notes", prog.Notes)
	} else if err := st.ReplaceDataset(ctx, ds); err != nil {
		_ = st.FinishRun(ctx, runID, "failed", prog.Calls, prog.Errors, append(prog.Notes, err.Error()))
		return err
	}
	log.Info("core done", "status", status, "routes", prog.Routes, "stops", prog.Stops, "trips", prog.Trips, "calls", prog.Calls, "errors", prog.Errors)
	return st.FinishRun(ctx, runID, status, prog.Calls, prog.Errors, prog.Notes)
}

func runFares(ctx context.Context, log *slog.Logger, st *sqlite.Store, sc *bmtc.Scraper, sh bmtc.Shard, resume bool) error {
	ds, err := st.LoadDataset(ctx, "bmtc")
	if err != nil {
		return err
	}
	if len(ds.Routes()) == 0 {
		log.Warn("no bmtc routes in store; run the core job first")
		return nil
	}
	runID, err := st.StartRun(ctx, "fares")
	if err != nil {
		return err
	}
	prog, err := sc.ScrapeFares(ctx, ds.Routes(), ds.Stops(), nil, &fareStore{st}, sh, resume)
	status := "ok"
	if err != nil {
		status = "failed"
	} else if prog.Errors > 0 {
		status = "partial"
	}
	log.Info("fares done", "status", status, "routes", prog.Routes, "calls", prog.Calls, "errors", prog.Errors)
	if ferr := st.FinishRun(ctx, runID, status, prog.Calls, prog.Errors, prog.Notes); ferr != nil && err == nil {
		err = ferr
	}
	return err
}

// fareStore adapts sqlite.Store to bmtc.FareStore (record type differs).
type fareStore struct{ *sqlite.Store }

func (f *fareStore) SaveFareSamples(ctx context.Context, routeSourceID string, samples []bmtc.FareSampleRec) error {
	out := make([]sqlite.FareSample, len(samples))
	for i, s := range samples {
		out[i] = sqlite.FareSample{FromCode: s.FromCode, ToCode: s.ToCode, ServiceTypeID: s.ServiceTypeID, FarePaise: s.FarePaise}
	}
	return f.Store.SaveFareSamples(ctx, routeSourceID, out)
}

func fmtSscanf(v string, i, n *int) (int, error) { return fmt.Sscanf(v, "%d/%d", i, n) }

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
