-- Multi-station support (for geographic anomaly detection) and security
-- flags on the scan log.

BEGIN;

CREATE TABLE airports (
    iata     CHAR(3) PRIMARY KEY,
    name     TEXT             NOT NULL,
    country  CHAR(2)          NOT NULL,
    lat      DOUBLE PRECISION NOT NULL,
    lon      DOUBLE PRECISION NOT NULL
);

INSERT INTO airports (iata, name, country, lat, lon) VALUES
    ('CGK', 'Soekarno-Hatta International', 'ID',  -6.1256, 106.6559),
    ('DPS', 'Ngurah Rai International',     'ID',  -8.7482, 115.1672),
    ('SIN', 'Singapore Changi',             'SG',   1.3644, 103.9915),
    ('MEL', 'Melbourne Tullamarine',        'AU', -37.6690, 144.8410),
    ('SYD', 'Sydney Kingsford Smith',       'AU', -33.9399, 151.1753),
    ('NRT', 'Tokyo Narita',                 'JP',  35.7720, 140.3929);

-- Every scan location belongs to a station; existing rows are the CGK hub.
ALTER TABLE warehouse_locations
    ADD COLUMN station CHAR(3) NOT NULL DEFAULT 'CGK' REFERENCES airports(iata);
ALTER TABLE warehouse_locations DROP CONSTRAINT warehouse_locations_grid_x_grid_y_key;
ALTER TABLE warehouse_locations ADD CONSTRAINT warehouse_locations_station_grid_key UNIQUE (station, grid_x, grid_y);

-- Outstation scan points (receiving + ramp) used by the network.
INSERT INTO warehouse_locations (id, zone, label, grid_x, grid_y, implied_status, station)
SELECT s.iata || '-' || z.zone || '-01', z.zone, s.iata || ' ' || z.name, z.x, 0, z.status::uld_status, s.iata
FROM (VALUES ('DPS'), ('SIN'), ('MEL'), ('SYD'), ('NRT')) AS s(iata)
CROSS JOIN (VALUES ('RCV', 'Receiving', 0, 'IN_TRANSIT'), ('RMP', 'Ramp', 1, 'LOADED')) AS z(zone, name, x, status);

-- Detections that fired for a scan (rule ids, e.g. {ULD-GEO-001}). Durable
-- record that survives log loss and lets analysts pivot from SIEM to DB.
ALTER TABLE uld_scan_events ADD COLUMN security_flags TEXT[] NOT NULL DEFAULT '{}';
CREATE INDEX uld_scan_events_flagged_idx ON uld_scan_events USING gin (security_flags)
    WHERE security_flags <> '{}';

COMMIT;
