package run

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var dbPath string

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	dbPath = filepath.Join(home, ".mdt", "test_results.db")
}

type TestResult struct {
	ID           int64
	Timestamp    time.Time
	TestFile     string
	TestName     string
	TestVersion  string
	Passed       bool
	DurationMs   int64
	PeakMemoryKB int64
	ErrorMsg     string
	ErrorDiff    string
	RunnerVer    string
}

type DB struct {
	conn *sql.DB
}

func NewDB() (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db dir: %w", err)
	}

	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open db: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate db: %w", err)
	}

	return db, nil
}

func (db *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS test_runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		test_file TEXT NOT NULL,
		test_name TEXT NOT NULL,
		test_version TEXT NOT NULL,
		passed BOOLEAN NOT NULL,
		duration_ms INTEGER NOT NULL,
		peak_memory_kb INTEGER,
		error_message TEXT,
		error_diff TEXT,
		runner_version TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_test_name ON test_runs(test_name, test_version);
	CREATE INDEX IF NOT EXISTS idx_timestamp ON test_runs(timestamp);
	CREATE INDEX IF NOT EXISTS idx_test_file ON test_runs(test_file);
	`
	_, err := db.conn.Exec(schema)
	return err
}

func (db *DB) Insert(result *TestResult) error {
	query := `
	INSERT INTO test_runs (test_file, test_name, test_version, passed, duration_ms, peak_memory_kb, error_message, error_diff, runner_version)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	res, err := db.conn.Exec(query,
		result.TestFile,
		result.TestName,
		result.TestVersion,
		result.Passed,
		result.DurationMs,
		result.PeakMemoryKB,
		result.ErrorMsg,
		result.ErrorDiff,
		result.RunnerVer,
	)
	if err != nil {
		return fmt.Errorf("failed to insert test result: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}
	result.ID = id
	return nil
}

func (db *DB) GetByTestName(testName, testVersion string) ([]TestResult, error) {
	query := `
	SELECT id, timestamp, test_file, test_name, test_version, passed, duration_ms, peak_memory_kb, error_message, error_diff, runner_version
	FROM test_runs
	WHERE test_name = ? AND test_version = ?
	ORDER BY timestamp DESC
	LIMIT 100
	`
	return db.query(query, testName, testVersion)
}

func (db *DB) GetSince(since string) ([]TestResult, error) {
	query := `
	SELECT id, timestamp, test_file, test_name, test_version, passed, duration_ms, peak_memory_kb, error_message, error_diff, runner_version
	FROM test_runs
	WHERE timestamp >= ?
	ORDER BY timestamp DESC
	`
	return db.query(query, since)
}

func (db *DB) GetAll(limit int) ([]TestResult, error) {
	query := `
	SELECT id, timestamp, test_file, test_name, test_version, passed, duration_ms, peak_memory_kb, error_message, error_diff, runner_version
	FROM test_runs
	ORDER BY timestamp DESC
	LIMIT ?
	`
	return db.query(query, limit)
}

func (db *DB) GetStats() (total int, passed int, failed int, avgDurationMs int64, err error) {
	row := db.conn.QueryRow(`
		SELECT 
			COUNT(*) as total,
			SUM(CASE WHEN passed THEN 1 ELSE 0 END) as passed,
			SUM(CASE WHEN NOT passed THEN 1 ELSE 0 END) as failed,
			AVG(duration_ms) as avg_duration
		FROM test_runs
	`)
	err = row.Scan(&total, &passed, &failed, &avgDurationMs)
	return
}

func (db *DB) query(query string, args ...interface{}) ([]TestResult, error) {
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query: %w", err)
	}
	defer rows.Close()

	var results []TestResult
	for rows.Next() {
		var r TestResult
		var timestamp sql.NullTime
		var errorMsg, errorDiff sql.NullString
		var peakMemoryKB sql.NullInt64

		err := rows.Scan(
			&r.ID,
			&timestamp,
			&r.TestFile,
			&r.TestName,
			&r.TestVersion,
			&r.Passed,
			&r.DurationMs,
			&peakMemoryKB,
			&errorMsg,
			&errorDiff,
			&r.RunnerVer,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		if timestamp.Valid {
			r.Timestamp = timestamp.Time
		}
		if errorMsg.Valid {
			r.ErrorMsg = errorMsg.String
		}
		if errorDiff.Valid {
			r.ErrorDiff = errorDiff.String
		}
		if peakMemoryKB.Valid {
			r.PeakMemoryKB = peakMemoryKB.Int64
		}

		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return results, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}
