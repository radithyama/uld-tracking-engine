-- V2: load planning (positions + balance data), network operations (routes,
-- flight lifecycle, in-flight status), security alert workflow, audit log.
--
-- Balance figures (arms, MAC, OEW, CG limits) are ILLUSTRATIVE approximations
-- for a portfolio demo. They are not certified data and must never be used for
-- real weight-and-balance.

-- Enum values can't be added inside a transaction block on older Postgres.
ALTER TYPE uld_status ADD VALUE IF NOT EXISTS 'IN_FLIGHT';

BEGIN;

-- ---------------------------------------------------------------- aircraft
ALTER TABLE aircrafts
    ADD COLUMN IF NOT EXISTS oew_kg        NUMERIC(10,1),
    ADD COLUMN IF NOT EXISTS oew_arm_m     NUMERIC(6,2),   -- operating empty weight CG, metres from datum
    ADD COLUMN IF NOT EXISTS lemac_m       NUMERIC(6,2),   -- leading edge of mean aerodynamic chord
    ADD COLUMN IF NOT EXISTS mac_m         NUMERIC(6,2),   -- MAC length
    ADD COLUMN IF NOT EXISTS cg_fwd_pct    NUMERIC(5,1),   -- zero-fuel CG envelope, %MAC
    ADD COLUMN IF NOT EXISTS cg_aft_pct    NUMERIC(5,1),
    ADD COLUMN IF NOT EXISTS cg_target_pct NUMERIC(5,1);   -- optimizer target (slightly aft = less trim drag)

-- Second A330 for the Melbourne rotation.
INSERT INTO aircrafts (registration, aircraft_type, max_payload_kg, max_uld_weight_kg, uld_positions)
VALUES ('PK-GPB', 'A330-300', 18000, 3400, 16)
ON CONFLICT (registration) DO NOTHING;

UPDATE aircrafts a SET oew_kg = v.oew, oew_arm_m = v.arm, lemac_m = v.lemac, mac_m = v.mac,
       cg_fwd_pct = v.fwd, cg_aft_pct = v.aft, cg_target_pct = v.tgt
FROM (VALUES
    ('PK-QZF',  41500.0, 17.29, 16.50, 3.96,  8.0, 32.0, 22.0),
    ('PK-GPA', 124000.0, 27.64, 25.60, 7.27, 14.0, 38.0, 30.0),
    ('PK-GPB', 124000.0, 27.64, 25.60, 7.27, 14.0, 38.0, 30.0),
    ('PK-GFA', 144400.0, 29.35, 27.50, 7.38, 15.0, 40.0, 28.0)
) AS v(reg, oew, arm, lemac, mac, fwd, aft, tgt)
WHERE a.registration = v.reg;

-- --------------------------------------------------------- cargo positions
CREATE TABLE IF NOT EXISTS aircraft_positions (
    aircraft_id    BIGINT        NOT NULL REFERENCES aircrafts(id) ON DELETE CASCADE,
    code           TEXT          NOT NULL,                   -- e.g. 11L, A, 51
    deck           TEXT          NOT NULL CHECK (deck IN ('MAIN', 'LOWER')),
    hold           TEXT          NOT NULL CHECK (hold IN ('MAIN', 'FWD', 'AFT', 'BULK')),
    arm_m          NUMERIC(6,2)  NOT NULL,                   -- balance arm of the position centroid
    side           SMALLINT      NOT NULL DEFAULT 0 CHECK (side IN (-1, 0, 1)),     -- -1 left / 0 centre / +1 right
    max_weight_kg  NUMERIC(8,1)  NOT NULL CHECK (max_weight_kg > 0),
    uld_types      TEXT[]        NOT NULL DEFAULT '{}',      -- empty = bulk only (no ULDs)
    seq            INTEGER       NOT NULL,
    PRIMARY KEY (aircraft_id, code)
);

-- B737-800BCF main deck: 11 single positions A-L (no I), any ULD type.
INSERT INTO aircraft_positions (aircraft_id, code, deck, hold, arm_m, side, max_weight_kg, uld_types, seq)
SELECT a.id, p.code, 'MAIN', 'MAIN', 7.0 + (p.n - 1) * 2.25, 0, 1500, '{AKE,AKH,PMC,PAG,PLA}', p.n
FROM aircrafts a
CROSS JOIN (VALUES ('A',1),('B',2),('C',3),('D',4),('E',5),('F',6),('G',7),('H',8),('J',9),('K',10),('L',11)) AS p(code, n)
WHERE a.registration = 'PK-QZF'
ON CONFLICT DO NOTHING;
INSERT INTO aircraft_positions (aircraft_id, code, deck, hold, arm_m, side, max_weight_kg, uld_types, seq)
SELECT a.id, b.code, 'LOWER', 'BULK', b.arm, 0, b.maxw, '{}', b.seq
FROM aircrafts a CROSS JOIN (VALUES ('FWD BULK', 11.5, 3500.0, 20), ('AFT BULK', 24.0, 4500.0, 21)) AS b(code, arm, maxw, seq)
WHERE a.registration = 'PK-QZF'
ON CONFLICT DO NOTHING;

-- A330-300 lower deck: FWD 11L-15R and AFT 31L-34R LD3 positions, bulk 51.
INSERT INTO aircraft_positions (aircraft_id, code, deck, hold, arm_m, side, max_weight_kg, uld_types, seq)
SELECT a.id, r.row || s.side, 'LOWER', r.hold, r.arm, s.lat, 1588, '{AKE,AKH}', r.seq * 2 + s.o
FROM aircrafts a
CROSS JOIN (VALUES (11,'FWD',12.0,1),(12,'FWD',13.6,2),(13,'FWD',15.2,3),(14,'FWD',16.8,4),(15,'FWD',18.4,5),
                   (31,'AFT',37.0,6),(32,'AFT',38.6,7),(33,'AFT',40.2,8),(34,'AFT',41.8,9)) AS r(row, hold, arm, seq)
CROSS JOIN (VALUES ('L', -1, 0), ('R', 1, 1)) AS s(side, lat, o)
WHERE a.registration IN ('PK-GPA', 'PK-GPB')
ON CONFLICT DO NOTHING;
INSERT INTO aircraft_positions (aircraft_id, code, deck, hold, arm_m, side, max_weight_kg, uld_types, seq)
SELECT a.id, '51', 'LOWER', 'BULK', 45.0, 0, 3468, '{}', 30
FROM aircrafts a WHERE a.registration IN ('PK-GPA', 'PK-GPB')
ON CONFLICT DO NOTHING;

-- B777F: 13 main-deck pallet positions A-P, lower FWD 11P-15P, AFT 31P-34P, bulk 51.
INSERT INTO aircraft_positions (aircraft_id, code, deck, hold, arm_m, side, max_weight_kg, uld_types, seq)
SELECT a.id, p.code, 'MAIN', 'MAIN', 8.0 + (p.n - 1) * 3.5, 0, 6804, '{PMC,PAG,PLA}', p.n
FROM aircrafts a
CROSS JOIN (VALUES ('A',1),('B',2),('C',3),('D',4),('E',5),('F',6),('G',7),('H',8),('J',9),('K',10),('L',11),('M',12),('P',13)) AS p(code, n)
WHERE a.registration = 'PK-GFA'
ON CONFLICT DO NOTHING;
INSERT INTO aircraft_positions (aircraft_id, code, deck, hold, arm_m, side, max_weight_kg, uld_types, seq)
SELECT a.id, l.code, 'LOWER', l.hold, l.arm, 0, 4626, '{PMC,PAG}', l.seq
FROM aircrafts a
CROSS JOIN (VALUES ('11P','FWD',14.0,20),('12P','FWD',16.5,21),('13P','FWD',19.0,22),('14P','FWD',21.5,23),('15P','FWD',24.0,24),
                   ('31P','AFT',40.0,25),('32P','AFT',42.5,26),('33P','AFT',45.0,27),('34P','AFT',47.5,28)) AS l(code, hold, arm, seq)
WHERE a.registration = 'PK-GFA'
ON CONFLICT DO NOTHING;
INSERT INTO aircraft_positions (aircraft_id, code, deck, hold, arm_m, side, max_weight_kg, uld_types, seq)
SELECT a.id, '51', 'LOWER', 'BULK', 51.5, 0, 4000, '{}', 40
FROM aircrafts a WHERE a.registration = 'PK-GFA'
ON CONFLICT DO NOTHING;

-- -------------------------------------------------------------- load plans
CREATE TABLE IF NOT EXISTS load_plans (
    flight_id     BIGINT      NOT NULL REFERENCES flights(id) ON DELETE CASCADE,
    position_code TEXT        NOT NULL,
    uld_id        TEXT        NOT NULL REFERENCES ulds(id) ON DELETE CASCADE,
    planned_by    TEXT        NOT NULL,
    planned_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (flight_id, position_code),
    UNIQUE (flight_id, uld_id)
);

-- ------------------------------------------------------- network / routes
-- Each aircraft flies an outbound/return pair from the CGK hub. Durations are
-- real-world minutes; the processor's network clock divides them by
-- NETWORK_TIME_SCALE so the demo network moves at watchable speed.
CREATE TABLE IF NOT EXISTS routes (
    flight_number      TEXT    PRIMARY KEY,
    aircraft_id        BIGINT  NOT NULL REFERENCES aircrafts(id),
    origin             CHAR(3) NOT NULL REFERENCES airports(iata),
    destination        CHAR(3) NOT NULL REFERENCES airports(iata),
    block_minutes      INTEGER NOT NULL CHECK (block_minutes > 0),
    turnaround_minutes INTEGER NOT NULL CHECK (turnaround_minutes > 0),
    return_flight      TEXT    NOT NULL,
    active             BOOLEAN NOT NULL DEFAULT true
);

INSERT INTO routes (flight_number, aircraft_id, origin, destination, block_minutes, turnaround_minutes, return_flight)
SELECT r.fn, a.id, r.o, r.d, r.blk, r.turn, r.ret
FROM (VALUES
    ('QZ7510', 'PK-QZF', 'CGK', 'SIN', 105,  75, 'QZ7511'),
    ('QZ7511', 'PK-QZF', 'SIN', 'CGK', 105,  75, 'QZ7510'),
    ('GA716',  'PK-GPA', 'CGK', 'SYD', 420, 120, 'GA717'),
    ('GA717',  'PK-GPA', 'SYD', 'CGK', 420, 120, 'GA716'),
    ('GA722',  'PK-GPB', 'CGK', 'MEL', 400, 120, 'GA723'),
    ('GA723',  'PK-GPB', 'MEL', 'CGK', 400, 120, 'GA722'),
    ('GA880',  'PK-GFA', 'CGK', 'NRT', 420, 150, 'GA881'),
    ('GA881',  'PK-GFA', 'NRT', 'CGK', 420, 150, 'GA880')
) AS r(fn, reg, o, d, blk, turn, ret)
JOIN aircrafts a ON a.registration = r.reg
ON CONFLICT (flight_number) DO NOTHING;

ALTER TABLE flights
    ADD COLUMN IF NOT EXISTS status            TEXT NOT NULL DEFAULT 'SCHEDULED',
    ADD COLUMN IF NOT EXISTS block_minutes     INTEGER,
    ADD COLUMN IF NOT EXISTS departed_at       TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS estimated_arrival TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS arrived_at        TIMESTAMPTZ;
DO $$ BEGIN
    ALTER TABLE flights ADD CONSTRAINT flights_status_check
        CHECK (status IN ('SCHEDULED', 'DEPARTED', 'ARRIVED', 'CANCELLED'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
CREATE INDEX IF NOT EXISTS flights_status_dep_idx ON flights (status, scheduled_departure);

-- The 002 seed departures are hours out; pull them within minutes so the
-- network starts rotating soon after an install. (Past ones are left alone:
-- the network clock departs them on its first tick.)
UPDATE flights f SET scheduled_departure = date_trunc('second', now()) + make_interval(mins => 3 + 3 * s.n)
FROM (VALUES ('QZ7510', 0), ('GA716', 1), ('GA880', 2)) AS s(fn, n)
WHERE f.flight_number = s.fn AND f.status = 'SCHEDULED'
  AND f.scheduled_departure > now() + interval '15 minutes';

-- Outstations get a 3x3 floor (receiving / staging / ramp). 003 created
-- <IATA>-RCV-01 at (0,0) and <IATA>-RMP-01 at (1,0); move ramp to row 2.
UPDATE warehouse_locations SET grid_x = 0, grid_y = 2
WHERE station <> 'CGK' AND zone = 'RMP' AND id LIKE '%-RMP-01';
INSERT INTO warehouse_locations (id, zone, label, grid_x, grid_y, implied_status, station)
SELECT s.iata || '-' || z.zone || '-' || lpad(x::text, 2, '0'), z.zone, s.iata || ' ' || z.name || ' ' || x,
       x - 1, z.row, z.status::uld_status, s.iata
FROM (VALUES ('DPS'), ('SIN'), ('MEL'), ('SYD'), ('NRT')) AS s(iata)
CROSS JOIN (VALUES ('RCV', 'Receiving', 0, 'IN_TRANSIT'), ('STA', 'Staging', 1, 'STAGED'), ('RMP', 'Ramp', 2, 'LOADED')) AS z(zone, name, row, status)
CROSS JOIN generate_series(1, 3) AS x
ON CONFLICT DO NOTHING;

-- ------------------------------------------------- security alert workflow
CREATE TABLE IF NOT EXISTS security_alerts (
    id           UUID        PRIMARY KEY,           -- ECS event.id, set by the emitting service
    occurred_at  TIMESTAMPTZ NOT NULL,
    rule_id      TEXT        NOT NULL,
    rule_name    TEXT        NOT NULL,
    action       TEXT        NOT NULL,
    kind         TEXT        NOT NULL,              -- alert | event
    severity     INTEGER     NOT NULL,
    service      TEXT        NOT NULL,
    reason       TEXT        NOT NULL DEFAULT '',
    source_ip    TEXT,
    principal    TEXT,
    uld_id       TEXT,
    details      JSONB       NOT NULL DEFAULT '{}',
    status       TEXT        NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'ACKNOWLEDGED', 'RESOLVED')),
    acked_by     TEXT,
    acked_at     TIMESTAMPTZ,
    resolved_by  TEXT,
    resolved_at  TIMESTAMPTZ,
    note         TEXT
);
CREATE INDEX IF NOT EXISTS security_alerts_time_idx   ON security_alerts (occurred_at DESC);
CREATE INDEX IF NOT EXISTS security_alerts_status_idx ON security_alerts (status, occurred_at DESC);

-- Privileged actions taken through the dashboard.
CREATE TABLE IF NOT EXISTS audit_log (
    id         BIGSERIAL   PRIMARY KEY,
    at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor      TEXT        NOT NULL,
    role       TEXT        NOT NULL,
    action     TEXT        NOT NULL,
    target     TEXT        NOT NULL DEFAULT '',
    details    JSONB       NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS audit_log_at_idx ON audit_log (at DESC);

COMMIT;
