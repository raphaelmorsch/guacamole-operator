package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type monitoredConnection struct {
	ConnectionID   int64  `json:"connectionId,omitempty"`
	ConnectionName string `json:"connectionName"`
	RemoteHost     string `json:"remoteHost"`
}

type exporter struct {
	db          *sql.DB
	connections []monitoredConnection

	connectionSessions *prometheus.GaugeVec
	scrapeSuccess      prometheus.Gauge
	scrapeTimestamp    prometheus.Gauge
}

func newExporter(db *sql.DB, connections []monitoredConnection) *exporter {
	e := &exporter{
		db:          db,
		connections: connections,
	}

	e.connectionSessions = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "guacamole_connection_active_sessions",
			Help: "Active Guacamole sessions for monitored connections (end_date IS NULL).",
		},
		[]string{"connection_id", "connection_name", "remote_host"},
	)
	e.scrapeSuccess = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "guacamole_metrics_exporter_last_scrape_success",
		Help: "Whether the last MySQL scrape succeeded (1) or failed (0).",
	})
	e.scrapeTimestamp = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "guacamole_metrics_exporter_last_scrape_timestamp_seconds",
		Help: "Unix timestamp of the last MySQL scrape attempt.",
	})

	prometheus.MustRegister(
		e.connectionSessions,
		e.scrapeSuccess,
		e.scrapeTimestamp,
	)

	return e
}

func (e *exporter) countActiveSessions(ctx context.Context, conn monitoredConnection) (float64, error) {
	var count float64
	if conn.ConnectionID > 0 {
		err := e.db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM guacamole_connection_history
			WHERE end_date IS NULL AND connection_id = ?`,
			conn.ConnectionID,
		).Scan(&count)
		return count, err
	}

	err := e.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM guacamole_connection_history
		WHERE end_date IS NULL AND connection_name = ?`,
		conn.ConnectionName,
	).Scan(&count)
	return count, err
}

func (e *exporter) scrape(ctx context.Context) error {
	e.connectionSessions.Reset()

	for _, conn := range e.connections {
		count, err := e.countActiveSessions(ctx, conn)
		if err != nil {
			return err
		}

		connectionID := "0"
		if conn.ConnectionID > 0 {
			connectionID = strconv.FormatInt(conn.ConnectionID, 10)
		}

		e.connectionSessions.WithLabelValues(
			connectionID,
			conn.ConnectionName,
			conn.RemoteHost,
		).Set(count)
	}

	return nil
}

func (e *exporter) runScrapeLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	scrape := func() {
		e.scrapeTimestamp.Set(float64(time.Now().Unix()))
		if err := e.scrape(ctx); err != nil {
			e.scrapeSuccess.Set(0)
			log.Printf("scrape failed: %v", err)
			return
		}
		e.scrapeSuccess.Set(1)
	}

	scrape()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scrape()
		}
	}
}

func mysqlDSN() (string, error) {
	host := envOrDefault("MYSQL_HOSTNAME", "localhost")
	port := envOrDefault("MYSQL_PORT", "3306")
	database := envOrDefault("MYSQL_DATABASE", "guacamole_db")
	user := os.Getenv("MYSQL_USER")
	password := os.Getenv("MYSQL_PASSWORD")
	if user == "" || password == "" {
		return "", fmt.Errorf("MYSQL_USER and MYSQL_PASSWORD are required")
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true", user, password, host, port, database), nil
}

func loadMonitoredConnections() []monitoredConnection {
	raw := os.Getenv("MONITORED_CONNECTIONS")
	if raw == "" {
		return nil
	}

	var connections []monitoredConnection
	if err := json.Unmarshal([]byte(raw), &connections); err != nil {
		log.Printf("invalid MONITORED_CONNECTIONS: %v", err)
		return nil
	}
	return connections
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envIntOrDefault(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func main() {
	port := envIntOrDefault("METRICS_PORT", 9110)
	intervalSeconds := envIntOrDefault("SCRAPE_INTERVAL_SECONDS", 15)
	connections := loadMonitoredConnections()

	dsn, err := mysqlDSN()
	if err != nil {
		log.Fatalf("mysql config: %v", err)
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("open mysql: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	exp := newExporter(db, connections)
	go exp.runScrapeLoop(ctx, time.Duration(intervalSeconds)*time.Second)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	addr := fmt.Sprintf(":%d", port)
	log.Printf("listening on %s (scrape interval %ds, monitored connections %d)", addr, intervalSeconds, len(connections))
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("http server: %v", err)
	}
}
