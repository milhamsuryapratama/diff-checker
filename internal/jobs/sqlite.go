package jobs

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/milhamsuryapratama/diff-checker/internal/agentic"
	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
	"github.com/milhamsuryapratama/diff-checker/internal/trace"
)

// Jobs were held in memory, which meant a restart lost every comparison anyone
// had run. SQLite is the right size for this: the data is small, strictly
// single-writer, and wants to live in one file next to the uploads rather than
// in a service someone has to operate.
//
// modernc.org/sqlite is a pure-Go translation of SQLite, so the binary still
// cross-compiles and still runs in a scratch container — no cgo, no libsqlite3
// to install.

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
    id           TEXT PRIMARY KEY,
    status       TEXT NOT NULL,
    prev_name    TEXT NOT NULL,
    curr_name    TEXT NOT NULL,
    prev_path    TEXT NOT NULL,
    curr_path    TEXT NOT NULL,
    use_ai       INTEGER NOT NULL,
    steps        TEXT NOT NULL,
    report       TEXT,
    usage        TEXT,
    cost_usd     REAL NOT NULL DEFAULT 0,
    error        TEXT,
    created_at   TEXT NOT NULL,
    finished_at  TEXT
);

CREATE TABLE IF NOT EXISTS traces (
    job_id  TEXT NOT NULL,
    seq     INTEGER NOT NULL,
    node    TEXT NOT NULL,
    kind    TEXT NOT NULL,
    text    TEXT NOT NULL,
    at      TEXT NOT NULL,
    PRIMARY KEY (job_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_jobs_created ON jobs(created_at DESC);
`

// Open prepares the database at path, creating it if needed.
//
// WAL keeps a reader — the report page, the SSE stream — from blocking the
// worker mid-write, which is the only concurrency this application has.
// busy_timeout turns the remaining lock contention into a short wait rather
// than an immediate "database is locked" error.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("membuka basis data %s: %w", path, err)
	}
	// One writer. SQLite serialises writes anyway, and letting the pool open
	// several connections only converts that into lock errors.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("menyiapkan skema: %w", err)
	}
	return db, nil
}

// persist writes a job's current state. Called on every mutation, which is
// cheap at this scale and removes any question of what is durable and when.
func (s *Store) persist(j *Job) {
	if s.db == nil {
		return
	}
	steps, _ := json.Marshal(j.Steps)
	var report, usage []byte
	if j.Report != nil {
		report, _ = json.Marshal(j.Report)
	}
	if len(j.Usage) > 0 {
		usage, _ = json.Marshal(j.Usage)
	}
	var finished any
	if !j.FinishedAt.IsZero() {
		finished = j.FinishedAt.Format(time.RFC3339Nano)
	}

	_, err := s.db.Exec(`
        INSERT INTO jobs (id, status, prev_name, curr_name, prev_path, curr_path,
                          use_ai, steps, report, usage, cost_usd, error,
                          created_at, finished_at)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT(id) DO UPDATE SET
            status=excluded.status, steps=excluded.steps, report=excluded.report,
            usage=excluded.usage, cost_usd=excluded.cost_usd, error=excluded.error,
            finished_at=excluded.finished_at`,
		j.ID, string(j.Status), j.PrevName, j.CurrName, j.prevPath, j.currPath,
		boolInt(j.UseAI), string(steps), nullString(report), nullString(usage),
		j.CostUSD, j.Error, j.CreatedAt.Format(time.RFC3339Nano), finished)
	if err != nil && s.log != nil {
		s.log.Error("gagal menyimpan job", "job", j.ID, "err", err)
	}
}

// AppendTrace stores one trace entry.
//
// Streamed reasoning arrives as a growing entry with a stable seq, so this
// upserts rather than inserts: the row ends up holding the complete line
// whether the job finished normally or the process died halfway through it.
func (s *Store) AppendTrace(jobID string, e trace.Entry) {
	if s.db == nil {
		return
	}
	_, err := s.db.Exec(`
        INSERT INTO traces (job_id, seq, node, kind, text, at) VALUES (?,?,?,?,?,?)
        ON CONFLICT(job_id, seq) DO UPDATE SET text=excluded.text`,
		jobID, e.Seq, e.Node, string(e.Kind), e.Text, e.At.Format(time.RFC3339Nano))
	if err != nil && s.log != nil {
		s.log.Error("gagal menyimpan jejak", "job", jobID, "err", err)
	}
}

// LoadTrace returns a job's recorded reasoning in order.
func (s *Store) LoadTrace(jobID string) ([]trace.Entry, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT seq, node, kind, text, at FROM traces WHERE job_id = ? ORDER BY seq`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []trace.Entry
	for rows.Next() {
		var e trace.Entry
		var kind, at string
		if err := rows.Scan(&e.Seq, &e.Node, &kind, &e.Text, &at); err != nil {
			return nil, err
		}
		e.Kind = trace.Kind(kind)
		e.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// restore loads persisted jobs back into memory at startup.
//
// A job that was mid-flight when the process stopped cannot be resumed: its
// pipeline state lived in the executor, not the database. Marking it failed is
// the honest outcome — leaving it "running" would show a progress page that
// never advances, which is worse than an error the user can act on.
func (s *Store) restore() error {
	if s.db == nil {
		return nil
	}
	rows, err := s.db.Query(`
        SELECT id, status, prev_name, curr_name, prev_path, curr_path, use_ai,
               steps, report, usage, cost_usd, error, created_at, finished_at
        FROM jobs ORDER BY created_at DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var interrupted []*Job
	for rows.Next() {
		var (
			j                    Job
			status, steps        string
			report, usage, errTx sql.NullString
			created              string
			finished             sql.NullString
			useAI                int
		)
		if err := rows.Scan(&j.ID, &status, &j.PrevName, &j.CurrName, &j.prevPath,
			&j.currPath, &useAI, &steps, &report, &usage, &j.CostUSD, &errTx,
			&created, &finished); err != nil {
			return err
		}
		j.Status = Status(status)
		j.UseAI = useAI == 1
		j.Error = errTx.String
		j.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if finished.Valid {
			j.FinishedAt, _ = time.Parse(time.RFC3339Nano, finished.String)
		}
		_ = json.Unmarshal([]byte(steps), &j.Steps)
		if report.Valid && report.String != "" {
			var r docmodel.Report
			if json.Unmarshal([]byte(report.String), &r) == nil {
				j.Report = &r
			}
		}
		if usage.Valid && usage.String != "" {
			var u []agentic.NodeUsage
			if json.Unmarshal([]byte(usage.String), &u) == nil {
				j.Usage = u
			}
		}

		if j.Status == StatusRunning || j.Status == StatusQueued {
			j.Status = StatusFailed
			j.Error = "server dimulai ulang saat perbandingan ini berjalan; jalankan ulang untuk melanjutkan"
			j.FinishedAt = time.Now()
			for i := range j.Steps {
				if j.Steps[i].Status == "pending" || j.Steps[i].Status == "running" {
					j.Steps[i].Status = "error"
				}
			}
			interrupted = append(interrupted, &j)
		}

		cp := j
		s.jobs[j.ID] = &cp
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, j := range interrupted {
		s.persist(j)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullString(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}
