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

// Migrations represents all available migrations
var Migrations = []struct {
	Version int
	SQL     string
}{
	{
		Version: 1,
		SQL:     SchemaV1,
	},
}
