-- namma-transit SQLite schema. The scraper is the only writer; the server
-- opens read-only. Normalised tables are re-derivable from raw_responses.

CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS scrape_runs (
  id          INTEGER PRIMARY KEY,
  job         TEXT NOT NULL,
  started_at  TEXT NOT NULL,
  finished_at TEXT,
  status      TEXT NOT NULL DEFAULT 'running', -- running | ok | partial | failed
  calls       INTEGER NOT NULL DEFAULT 0,
  errors      INTEGER NOT NULL DEFAULT 0,
  notes       TEXT
);

CREATE TABLE IF NOT EXISTS raw_responses (
  id          INTEGER PRIMARY KEY,
  endpoint    TEXT NOT NULL,
  params_hash TEXT NOT NULL,
  params_json TEXT NOT NULL,
  status      INTEGER NOT NULL,
  body        BLOB,
  fetched_at  TEXT NOT NULL,
  run_id      INTEGER REFERENCES scrape_runs(id)
);
CREATE INDEX IF NOT EXISTS ix_raw_latest ON raw_responses(endpoint, params_hash, fetched_at DESC);

CREATE TABLE IF NOT EXISTS service_types (
  id            INTEGER PRIMARY KEY,
  name          TEXT NOT NULL,
  service_class TEXT NOT NULL
);

-- One row per route direction (pattern). source_id is the upstream identity.
CREATE TABLE IF NOT EXISTS routes (
  id            INTEGER PRIMARY KEY,
  dataset       TEXT NOT NULL,             -- bmtc | bmrcl
  source        TEXT NOT NULL,             -- gtfs | scrape
  source_id     TEXT NOT NULL,
  short_name    TEXT NOT NULL,
  headsign      TEXT NOT NULL DEFAULT '',
  mode          INTEGER NOT NULL,          -- domain.Mode
  service_class INTEGER NOT NULL,          -- domain.ServiceClass
  line_color    TEXT NOT NULL DEFAULT '',
  updated_at    TEXT NOT NULL,
  UNIQUE(dataset, source_id)
);
CREATE INDEX IF NOT EXISTS ix_routes_short ON routes(short_name);

CREATE TABLE IF NOT EXISTS stops (
  id        INTEGER PRIMARY KEY,
  dataset   TEXT NOT NULL,
  source    TEXT NOT NULL,
  source_id TEXT NOT NULL,
  name      TEXT NOT NULL,
  lat       REAL NOT NULL,
  lng       REAL NOT NULL,
  kind      INTEGER NOT NULL,              -- domain.StopKind
  sub_label TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL,
  UNIQUE(dataset, source_id)
);
CREATE INDEX IF NOT EXISTS ix_stops_name ON stops(name COLLATE NOCASE);
CREATE INDEX IF NOT EXISTS ix_stops_cell ON stops(CAST(lat*200 AS INTEGER), CAST(lng*200 AS INTEGER));

CREATE TABLE IF NOT EXISTS route_stops (
  route_id INTEGER NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
  seq      INTEGER NOT NULL,
  stop_id  INTEGER NOT NULL REFERENCES stops(id),
  dist_km  REAL NOT NULL DEFAULT 0,
  PRIMARY KEY (route_id, seq)
);
CREATE INDEX IF NOT EXISTS ix_route_stops_stop ON route_stops(stop_id);

-- Explicit trips: arr/dep are packed little-endian int32 seconds, one pair
-- of arrays per trip, so a bmtc-sized feed stays a few tens of MB.
CREATE TABLE IF NOT EXISTS trips (
  id       INTEGER PRIMARY KEY,
  route_id INTEGER NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
  days     INTEGER NOT NULL,               -- domain.DayMask
  dep0     INTEGER NOT NULL,               -- first departure, for ordering
  arr      BLOB NOT NULL,
  dep      BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_trips_route ON trips(route_id, dep0);

CREATE TABLE IF NOT EXISTS headways (
  route_id  INTEGER NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
  day       INTEGER NOT NULL,
  from_sec  INTEGER NOT NULL,
  to_sec    INTEGER NOT NULL,
  every_sec INTEGER NOT NULL,
  PRIMARY KEY (route_id, day, from_sec)
);

CREATE TABLE IF NOT EXISTS footpaths (
  from_stop INTEGER NOT NULL REFERENCES stops(id),
  to_stop   INTEGER NOT NULL REFERENCES stops(id),
  seconds   INTEGER NOT NULL,
  PRIMARY KEY (from_stop, to_stop)
);

CREATE TABLE IF NOT EXISTS fare_samples (
  route_source_id TEXT NOT NULL,
  from_stop_code  TEXT NOT NULL,
  to_stop_code    TEXT NOT NULL,
  service_type_id INTEGER NOT NULL,
  fare_paise      INTEGER NOT NULL,
  fetched_at      TEXT NOT NULL,
  PRIMARY KEY (route_source_id, from_stop_code, to_stop_code, service_type_id)
);

CREATE TABLE IF NOT EXISTS fare_stage_boundaries (
  route_id   INTEGER NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
  stage_no   INTEGER NOT NULL,
  stop_pos   INTEGER NOT NULL,
  derived_at TEXT NOT NULL,
  PRIMARY KEY (route_id, stage_no)
);
