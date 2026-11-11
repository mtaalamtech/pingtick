package main

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

var db *sql.DB

const databaseName = "pingtick.db"

// databasePath keeps the database next to the program, so a copy extracted
// from a zip works wherever it is started from.
func databasePath() string {
	exe, err := os.Executable()
	if err != nil {
		return databaseName
	}
	return databasePathFor(filepath.Dir(exe), os.TempDir(), os.Getenv("LOCALAPPDATA"))
}

// databasePathFor picks the database location for a program in exeDir.
//   - "go run" and "go test" build into a go-build folder, so those use the
//     current folder.
//   - A program opened straight from a zip runs from a temporary folder that
//     Windows clears later, and a folder such as Program Files cannot be
//     written to. Both keep their data in %LOCALAPPDATA%\PingTick instead.
func databasePathFor(exeDir, tempDir, localAppData string) string {
	inTemp := strings.HasPrefix(strings.ToLower(filepath.Clean(exeDir)), strings.ToLower(filepath.Clean(tempDir)))
	if inTemp && strings.Contains(strings.ToLower(exeDir), "go-build") {
		return databaseName
	}
	if !inTemp && folderWritable(exeDir) {
		return filepath.Join(exeDir, databaseName)
	}
	if localAppData != "" {
		dir := filepath.Join(localAppData, "PingTick")
		if os.MkdirAll(dir, 0o755) == nil {
			return filepath.Join(dir, databaseName)
		}
	}
	return databaseName
}

func folderWritable(dir string) bool {
	probe, err := os.CreateTemp(dir, ".pingtick-write-test-*")
	if err != nil {
		return false
	}
	probe.Close()
	os.Remove(probe.Name())
	return true
}

func InitDB() {
	var err error
	dbPath := databasePath()
	log.Printf("Using database %s", dbPath)

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
