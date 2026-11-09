package main

import (
	"database/sql"
	"log"

	_ "modernc.org/sqlite"
)

var db *sql.DB

func InitDB() {
	var err error
	dbPath := "./pingtick.db"

	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	db.SetMaxOpenConns(1)
	db.Exec("PRAGMA journal_mode=WAL;")
	db.Exec("PRAGMA busy_timeout=10000;")
	db.Exec("PRAGMA cache_size=-1024;") // limit the page cache to 1 MB

	// Create tables if they do not exist
	queries := []string{
		`CREATE TABLE IF NOT EXISTS targets (
			id TEXT PRIMARY KEY,
			name TEXT,
			host TEXT,
			type TEXT,
			interval INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS latency_history (
			target_id TEXT,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
			latency INTEGER,
			status TEXT,
			reply_ip TEXT,
			ttl INTEGER,
			FOREIGN KEY(target_id) REFERENCES targets(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT
		);`,
	}

	for _, q := range queries {
		_, err = db.Exec(q)
		if err != nil {
			log.Fatalf("Failed to execute migration query: %v", err)
		}
	}

	// Try to alter table for backwards compatibility
	db.Exec("ALTER TABLE latency_history ADD COLUMN reply_ip TEXT")
	db.Exec("ALTER TABLE latency_history ADD COLUMN ttl INTEGER")
	db.Exec("ALTER TABLE targets ADD COLUMN tag TEXT")
	db.Exec("ALTER TABLE targets ADD COLUMN category TEXT")
	db.Exec("ALTER TABLE targets ADD COLUMN snmp_community TEXT")
	db.Exec("ALTER TABLE targets ADD COLUMN snmp_oid TEXT")
	db.Exec("ALTER TABLE targets ADD COLUMN wmi_user TEXT")
	db.Exec("ALTER TABLE targets ADD COLUMN wmi_pass TEXT")
	db.Exec("ALTER TABLE targets ADD COLUMN wmi_namespace TEXT")
	db.Exec("ALTER TABLE targets ADD COLUMN wmi_query TEXT")

	db.Exec("CREATE INDEX IF NOT EXISTS idx_history_target_time ON latency_history(target_id, timestamp)")
	db.Exec("CREATE INDEX IF NOT EXISTS idx_history_time ON latency_history(timestamp)")

	// Pre-population of targets has been disabled for production releases
}

func CloseDB() {
	if db != nil {
		db.Close()
	}
}
