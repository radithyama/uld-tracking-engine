-- Demo reference data. The simulator (cmd/simulator) generates scans for the
-- same ULD ids, so keep the two in sync if you change the naming here.

BEGIN;

INSERT INTO aircrafts (registration, aircraft_type, max_payload_kg, max_uld_weight_kg, uld_positions) VALUES
    ('PK-GFA', 'B777F',        102000, 6804, 27),
    ('PK-GPA', 'A330-300',      18000, 3400, 16),
    ('PK-QZF', 'B737-800BCF',    8000, 1500, 11);

INSERT INTO flights (flight_number, aircraft_id, origin, destination, scheduled_departure)
SELECT f.flight_number, a.id, f.origin, f.destination, now() + f.dep_offset
FROM (VALUES
    ('GA880',  'PK-GFA', 'CGK', 'NRT', interval '3 hours'),
    ('GA716',  'PK-GPA', 'CGK', 'SYD', interval '5 hours'),
    ('QZ7510', 'PK-QZF', 'CGK', 'SIN', interval '2 hours')
) AS f(flight_number, registration, origin, destination, dep_offset)
JOIN aircrafts a ON a.registration = f.registration;

-- 6 x 5 warehouse grid. Rows are zones, flowing top (landside) to bottom (airside).
INSERT INTO warehouse_locations (id, zone, label, grid_x, grid_y, implied_status)
SELECT z.zone || '-' || lpad(x::text, 2, '0'),
       z.zone,
       z.name || ' ' || x,
       x - 1,
       z.row,
       z.status::uld_status
FROM (VALUES
    ('RCV', 'Receiving',  0, 'IN_TRANSIT'),
    ('BUP', 'Build-up',   1, 'IN_TRANSIT'),
    ('STA', 'Staging A',  2, 'STAGED'),
    ('STB', 'Staging B',  3, 'STAGED'),
    ('RMP', 'Ramp',       4, 'LOADED')
) AS z(zone, name, row, status)
CROSS JOIN generate_series(1, 6) AS x;

-- 12 lower-deck containers (AKE / LD3) and 8 main-deck pallets (PMC).
INSERT INTO ulds (id, uld_type, tare_weight_kg, max_gross_weight_kg, current_weight_kg, location_id, flight_id, status)
SELECT 'AKE' || (10000 + n) || 'GA', 'AKE', 82, 1588, 82 + (n * 47) % 900,
       'RCV-' || lpad(((n - 1) % 6 + 1)::text, 2, '0'),
       (SELECT id FROM flights WHERE flight_number = CASE WHEN n % 2 = 0 THEN 'GA716' ELSE 'QZ7510' END),
       'IN_TRANSIT'
FROM generate_series(1, 12) AS n;

INSERT INTO ulds (id, uld_type, tare_weight_kg, max_gross_weight_kg, current_weight_kg, location_id, flight_id, status)
SELECT 'PMC' || (20000 + n) || 'GA', 'PMC', 110, 6804, 110 + (n * 613) % 4500,
       'BUP-' || lpad(((n - 1) % 6 + 1)::text, 2, '0'),
       (SELECT id FROM flights WHERE flight_number = 'GA880'),
       'IN_TRANSIT'
FROM generate_series(1, 8) AS n;

COMMIT;
