// Package storage provides job persistence using SQLite.
package storage

// Schema definitions for job persistence database
const (
	// SchemaV1 is the initial database schema
	SchemaV1 = `
CREATE TABLE IF NOT EXISTS jobs (
	id TEXT PRIMARY KEY,
	status TEXT NOT NULL,
	request_json TEXT NOT NULL,
	progress_json TEXT,
	error_message TEXT,
	retry_count INTEGER DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	completed_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
CREATE INDEX IF NOT EXISTS idx_jobs_created_at ON jobs(created_at);
CREATE INDEX IF NOT EXISTS idx_jobs_updated_at ON jobs(updated_at);

CREATE TABLE IF NOT EXISTS schema_version (
	version INTEGER PRIMARY KEY,
	applied_at INTEGER NOT NULL
);
`
)

// SchemaV2 adds stage_rates table for progress estimation
const SchemaV2 = `
CREATE TABLE IF NOT EXISTS stage_rates (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	stage TEXT NOT NULL,
	rate_bps REAL NOT NULL,
	bytes_processed INTEGER NOT NULL,
	duration_ms INTEGER NOT NULL,
	job_id TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	FOREIGN KEY (job_id) REFERENCES jobs(id)
);

CREATE INDEX IF NOT EXISTS idx_stage_rates_stage ON stage_rates(stage);
CREATE INDEX IF NOT EXISTS idx_stage_rates_created_at ON stage_rates(created_at);
`

// Migrations represents all available migrations
var Migrations = []struct {
	Version int
	SQL     string
}{
	{
		Version: 1,
		SQL:     SchemaV1,
	},
	{
		Version: 2,
		SQL:     SchemaV2,
	},
}
