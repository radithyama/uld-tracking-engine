// Command verify reconciles a chaos-test run end to end and exits non-zero on
// any data loss or inconsistency:
//
//  1. pauses the simulator and waits for every buffer to drain
//     (simulator queue -> ingest spool -> Kafka -> processor);
//  2. loads the simulator's ledger: every event id ingest acknowledged (202);
//  3. checks each id exists exactly once in uld_scan_events (no loss, no dupes);
//  4. checks each ULD's current state reflects its newest scan (ordering);
//  5. checks the Redis projection matches Postgres (version per ULD).
//
// Usage (inside the compose network):
//
//	docker compose --profile tools run --rm verify
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/radithya/uld-engine/internal/config"
)

type endpoints struct{ sim, ingest, processor, realtime string }

var httpc = &http.Client{Timeout: 10 * time.Second}

func main() {
	var e endpoints
	flag.StringVar(&e.sim, "sim", config.String("SIM_URL", "http://localhost:8090"), "simulator control API")
	flag.StringVar(&e.ingest, "ingest", config.String("INGEST_BASE_URL", "http://localhost:8080"), "ingest API")
	flag.StringVar(&e.processor, "processor", config.String("PROCESSOR_URL", "http://localhost:9090"), "processor metrics")
	flag.StringVar(&e.realtime, "realtime", config.String("REALTIME_URL", "http://localhost:8081"), "realtime API")
	timeout := flag.Duration("timeout", config.Duration("VERIFY_TIMEOUT", 5*time.Minute), "max time to wait for drain")
	noPause := flag.Bool("no-pause", false, "do not pause the simulator first")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout+time.Minute)
	defer cancel()
	ok := run(ctx, e, *timeout, !*noPause)
	if !ok {
		os.Exit(1)
	}
}

func run(ctx context.Context, e endpoints, timeout time.Duration, pause bool) bool {
	pass := true
	check := func(name string, good bool, detail string) {
		mark := "PASS"
		if !good {
			mark, pass = "FAIL", false
		}
		fmt.Printf("  [%s] %-46s %s\n", mark, name, detail)
	}

	fmt.Println("== 1. Quiesce")
	if pause {
		must(postJSON(e.sim + "/pause"))
		fmt.Println("  simulator generation paused")
	}
	deadline := time.Now().Add(timeout)
	for {
		sim, ing := getMap(e.sim+"/stats"), getMap(e.ingest+"/readyz")
		simDrained, _ := sim["drained"].(bool)
		spool := num(ing["spool_pending"])
		if simDrained && spool == 0 {
			fmt.Printf("  simulator queue and ingest spool drained\n")
			break
		}
		if time.Now().After(deadline) {
			check("buffers drained", false, fmt.Sprintf("sim unsent=%v spool_pending=%v", sim["unsent"], spool))
			return false
		}
		fmt.Printf("  waiting: sim unsent=%v, ingest spool_pending=%v\n", sim["unsent"], spool)
		time.Sleep(2 * time.Second)
	}

	ids := fetchLedger(e.sim + "/sent")
	sim := getMap(e.sim + "/stats")
	fmt.Printf("== 2. Ledger: %d scans acknowledged by ingest (generated=%v, accepted via spool=%v, client retries=%v)\n",
		len(ids), sim["generated"], sim["accepted_spool"], sim["retries"])
	distinct := map[string]struct{}{}
	for _, id := range ids {
		distinct[id] = struct{}{}
	}
	check("ledger has no duplicate ids", len(distinct) == len(ids), fmt.Sprintf("%d ids, %d distinct", len(ids), len(distinct)))
	check("every generated scan was acknowledged", num(sim["generated"]) == float64(len(ids)),
		fmt.Sprintf("generated=%v acked=%d rejected=%v", sim["generated"], len(ids), sim["rejected"]))

	pool, err := pgxpool.New(ctx, config.String("DATABASE_URL", "postgres://uld:uld@localhost:5432/uld?sslmode=disable"))
	must(err)
	defer pool.Close()

	fmt.Println("== 3. Delivery (ledger vs uld_scan_events)")
	var found int
	for {
		must(pool.QueryRow(ctx, `SELECT count(*) FROM uld_scan_events WHERE event_id = ANY($1::uuid[])`, ids).Scan(&found))
		if found == len(ids) || time.Now().After(deadline) {
			break
		}
		fmt.Printf("  processor catching up: %d/%d persisted\n", found, len(ids))
		time.Sleep(2 * time.Second)
	}
	var missing []string
	rows, err := pool.Query(ctx, `
		SELECT id::text FROM (
		    SELECT unnest($1::uuid[]) AS id EXCEPT SELECT event_id FROM uld_scan_events
		) m LIMIT 10`, ids)
	must(err)
	for rows.Next() {
		var id string
		must(rows.Scan(&id))
		missing = append(missing, id)
	}
	must(rows.Err())
	check("no acknowledged scan lost", found == len(ids), fmt.Sprintf("%d/%d persisted %s", found, len(ids), sample(missing)))

	var total, uniq int
	must(pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT event_id) FROM uld_scan_events WHERE event_id = ANY($1::uuid[])`, ids).Scan(&total, &uniq))
	check("no scan persisted twice", total == uniq, fmt.Sprintf("rows=%d distinct=%d", total, uniq))

	proc, ing := getMap(e.processor+"/metrics"), getMap(e.ingest+"/metrics")
	fmt.Printf("  kafka-level redeliveries absorbed by idempotency: %v (processor duplicates_absorbed)\n", proc["duplicates_absorbed"])
	fmt.Printf("  ingest: direct=%v spooled=%v replayed=%v corrupt=%v\n",
		ing["published_direct"], ing["spooled"], ing["replayed"], ing["spool_corrupt"])

	fmt.Println("== 4. Ordering (current state = newest scan)")
	var wrong int
	must(pool.QueryRow(ctx, `
		SELECT count(*) FROM ulds u
		JOIN LATERAL (SELECT max(scanned_at) AS newest FROM uld_scan_events e WHERE e.uld_id = u.id) l ON true
		WHERE l.newest IS NOT NULL AND u.last_scan_at IS DISTINCT FROM l.newest`).Scan(&wrong))
	check("each ULD reflects its newest scan", wrong == 0, fmt.Sprintf("%d ULDs out of order", wrong))

	fmt.Println("== 5. Projection (Redis via realtime vs Postgres)")
	pgVersions := map[string]int64{}
	rows, err = pool.Query(ctx, `SELECT id, version FROM ulds`)
	must(err)
	for rows.Next() {
		var id string
		var v int64
		must(rows.Scan(&id, &v))
		pgVersions[id] = v
	}
	must(rows.Err())
	var snap struct {
		Data struct {
			ULDs []struct {
				ID      string `json:"id"`
				Version int64  `json:"version"`
			} `json:"ulds"`
		} `json:"data"`
	}
	must(json.Unmarshal(getBody(e.realtime+"/api/snapshot"), &snap))
	mismatch := 0
	for _, u := range snap.Data.ULDs {
		if pgVersions[u.ID] != u.Version {
			mismatch++
		}
	}
	check("redis projection matches postgres", mismatch == 0 && len(snap.Data.ULDs) == len(pgVersions),
		fmt.Sprintf("%d ULDs, %d version mismatches", len(pgVersions), mismatch))

	if pause {
		must(postJSON(e.sim + "/resume"))
	}
	fmt.Println()
	if pass {
		fmt.Println("RESULT: PASS — zero loss, zero duplicates, ordered, consistent")
	} else {
		fmt.Println("RESULT: FAIL")
	}
	return pass
}

func sample(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return "missing e.g. " + strings.Join(ids, ",")
}

func fetchLedger(url string) []string {
	var ids []string
	for _, l := range strings.Split(string(getBody(url)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			ids = append(ids, l)
		}
	}
	return ids
}

func getBody(url string) []byte {
	resp, err := httpc.Get(url)
	must(err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	must(err)
	return b
}

func getMap(url string) map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal(getBody(url), &m)
	return m
}

func postJSON(url string) error {
	resp, err := httpc.Post(url, "application/json", nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "verify:", err)
		os.Exit(2)
	}
}
