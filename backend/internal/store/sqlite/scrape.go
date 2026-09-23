package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// StartRun records the beginning of a scraper job; raw responses saved
// until FinishRun are tagged with it.
func (s *Store) StartRun(ctx context.Context, job string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO scrape_runs(job, started_at) VALUES(?, ?)`, job, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	s.runID = sql.NullInt64{Int64: id, Valid: true}
	return id, nil
}

// FinishRun closes a run with its outcome.
func (s *Store) FinishRun(ctx context.Context, id int64, status string, calls, errs int, notes []string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE scrape_runs SET finished_at=?, status=?, calls=?, errors=?, notes=? WHERE id=?`,
		time.Now().UTC().Format(time.RFC3339), status, calls, errs, strings.Join(notes, "\n"), id)
	s.runID = sql.NullInt64{}
	return err
}

// LastRun returns the most recent run of a job, if any.
func (s *Store) LastRun(ctx context.Context, job string) (status string, finished time.Time, err error) {
	var fin sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT status, finished_at FROM scrape_runs WHERE job=? ORDER BY id DESC LIMIT 1`, job).Scan(&status, &fin)
	if err == sql.ErrNoRows {
		return "", time.Time{}, nil
	}
	if fin.Valid {
		finished, _ = time.Parse(time.RFC3339, fin.String)
	}
	return status, finished, err
}

// SaveRaw implements bmtc.RawStore.
func (s *Store) SaveRaw(ctx context.Context, endpoint, paramsHash, paramsJSON string, status int, body []byte, fetchedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO raw_responses(endpoint, params_hash, params_json, status, body, fetched_at, run_id) VALUES(?,?,?,?,?,?,?)`,
		endpoint, paramsHash, paramsJSON, status, body, fetchedAt.UTC().Format(time.RFC3339Nano), s.runID)
	return err
}

// RawCount is used by tests and Health to report archive size.
func (s *Store) RawCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM raw_responses`).Scan(&n)
	return n, err
}

// FareSample is one scraped stop-pair fare.
type FareSample struct {
	FromCode, ToCode string
	ServiceTypeID    int
	FarePaise        int32
}

// SaveFareSamples upserts samples for a route (by upstream source id).
func (s *Store) SaveFareSamples(ctx context.Context, routeSourceID string, samples []FareSample) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, f := range samples {
		if _, err := tx.ExecContext(ctx, `INSERT INTO fare_samples(route_source_id, from_stop_code, to_stop_code, service_type_id, fare_paise, fetched_at) VALUES(?,?,?,?,?,?)
			ON CONFLICT(route_source_id, from_stop_code, to_stop_code, service_type_id) DO UPDATE SET fare_paise=excluded.fare_paise, fetched_at=excluded.fetched_at`,
			routeSourceID, f.FromCode, f.ToCode, f.ServiceTypeID, f.FarePaise, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveStageBoundaries replaces the derived stage starts for a route.
func (s *Store) SaveStageBoundaries(ctx context.Context, dataset, routeSourceID string, starts []int) error {
	var rid int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM routes WHERE dataset=? AND source_id=?`, dataset, routeSourceID).Scan(&rid); err != nil {
		return fmt.Errorf("route %s/%s: %w", dataset, routeSourceID, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM fare_stage_boundaries WHERE route_id=?`, rid); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for i, pos := range starts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO fare_stage_boundaries(route_id, stage_no, stop_pos, derived_at) VALUES(?,?,?,?)`, rid, i+2, pos, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Fare cursor lets the weekly fare job resume where it stopped.
func (s *Store) FareCursor(ctx context.Context) (string, error) { return s.GetMeta(ctx, "fare_cursor") }
func (s *Store) SetFareCursor(ctx context.Context, v string) error {
	return s.SetMeta(ctx, "fare_cursor", v)
}
