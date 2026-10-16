-- ULD Tracking Engine — core schema.
-- Applied automatically by the Postgres container on first boot
-- (mounted into /docker-entrypoint-initdb.d).

BEGIN;

CREATE TYPE uld_status AS ENUM ('IN_TRANSIT', 'STAGED', 'LOADED');

-- Aircraft and their structural weight limits.
CREATE TABLE aircrafts (
    id                 BIGSERIAL PRIMARY KEY,
    registration       TEXT        NOT NULL UNIQUE,          -- e.g. PK-GPA
    aircraft_type      TEXT        NOT NULL,                 -- e.g. B777F
    max_payload_kg     NUMERIC(10,2) NOT NULL CHECK (max_payload_kg > 0),
    -- Max gross weight of a single ULD position on this airframe. A ULD whose
    -- weight exceeds this cannot be loaded even if the ULD itself is rated higher.
    max_uld_weight_kg  NUMERIC(10,2) NOT NULL CHECK (max_uld_weight_kg > 0),
    uld_positions      INTEGER     NOT NULL CHECK (uld_positions > 0),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE flights (
    id              BIGSERIAL PRIMARY KEY,
    flight_number   TEXT        NOT NULL,                    -- e.g. GA880
    aircraft_id     BIGINT      NOT NULL REFERENCES aircrafts(id),
    origin          CHAR(3)     NOT NULL,
    destination     CHAR(3)     NOT NULL,
    scheduled_departure TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (flight_number, scheduled_departure)
);
CREATE INDEX flights_number_dep_idx ON flights (flight_number, scheduled_departure DESC);

-- Physical scan points in the warehouse. grid_x/grid_y drive the dashboard map.
CREATE TABLE warehouse_locations (
    id              TEXT        PRIMARY KEY,                 -- e.g. STG-A03
    zone            TEXT        NOT NULL,                    -- RCV | BUP | STG | RMP
    label           TEXT        NOT NULL,
    grid_x          INTEGER     NOT NULL,
    grid_y          INTEGER     NOT NULL,
    -- Status a ULD implicitly takes on when scanned here (if the scan
    -- itself does not carry an explicit status).
    implied_status  uld_status  NOT NULL,
    UNIQUE (grid_x, grid_y)
);

-- Current (authoritative) state of every ULD.
CREATE TABLE ulds (
    id                   TEXT        PRIMARY KEY,            -- IATA ULD id, e.g. AKE12345GA
    uld_type             TEXT        NOT NULL,               -- AKE, PMC, ...
    tare_weight_kg       NUMERIC(10,2) NOT NULL DEFAULT 0,
    max_gross_weight_kg  NUMERIC(10,2) NOT NULL CHECK (max_gross_weight_kg > 0),
    current_weight_kg    NUMERIC(10,2) NOT NULL DEFAULT 0 CHECK (current_weight_kg >= 0),
    location_id          TEXT        REFERENCES warehouse_locations(id),
    flight_id            BIGINT      REFERENCES flights(id),
    status               uld_status  NOT NULL DEFAULT 'IN_TRANSIT',
    last_scanner_id      TEXT,
    last_scan_at         TIMESTAMPTZ,
    -- Monotonic per-ULD counter; lets consumers (dashboard) discard stale updates.
    version              BIGINT      NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ulds_flight_idx   ON ulds (flight_id);
CREATE INDEX ulds_location_idx ON ulds (location_id);

-- Append-only audit log of every scan. event_id gives idempotency against
-- Kafka at-least-once redelivery. (Partition by month for production volumes.)
CREATE TABLE uld_scan_events (
    event_id      UUID        PRIMARY KEY,
    uld_id        TEXT        NOT NULL,
    scanner_id    TEXT        NOT NULL,
    scan_type     TEXT        NOT NULL,                      -- RFID | BARCODE
    weight_kg     NUMERIC(10,2) NOT NULL,
    location_id   TEXT        NOT NULL,
    flight_number TEXT,
    status        uld_status,
    scanned_at    TIMESTAMPTZ NOT NULL,
    ingested_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    kafka_partition INTEGER,
    kafka_offset    BIGINT
);
CREATE INDEX uld_scan_events_uld_time_idx ON uld_scan_events (uld_id, scanned_at DESC);

COMMIT;
