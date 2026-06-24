package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"extract_coparn/internal/domain"

	_ "github.com/lib/pq"
)

type Repository struct {
	db *sql.DB
}

type FileDispatchInput struct {
	FileCodigo    string
	NombreArchivo string
	Ruta          string
	SourceDate    *time.Time
	Estado        domain.FileState
}

type RetryRow struct {
	ID          int64
	FileCodigo  string
	Estado      domain.FileState
	Intentos    int
	UltimoError string
}

type DispatchRecord struct {
	FileCodigo    string
	NombreArchivo string
	Ruta          string
	Estado        domain.FileState
	Intentos      int
}

func New(ctx context.Context, dsn string) (*Repository, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	return &Repository{db: db}, nil
}

func (r *Repository) Close() error {
	return r.db.Close()
}

func (r *Repository) FileExists(ctx context.Context, fileCodigo string) (bool, error) {
	const q = `SELECT 1 FROM file_dispatch WHERE file_codigo=$1 LIMIT 1`
	var v int
	err := r.db.QueryRowContext(ctx, q, fileCodigo).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repository) InsertPending(ctx context.Context, in FileDispatchInput) (bool, error) {
	const q = `INSERT INTO file_dispatch
	(file_codigo, nombre_archivo, ruta, source_date, estado, intentos, created_at, updated_at)
	VALUES ($1,$2,$3,$4,$5,0,NOW(),NOW())
	ON CONFLICT (file_codigo) DO NOTHING`
	res, err := r.db.ExecContext(ctx, q, in.FileCodigo, in.NombreArchivo, in.Ruta, in.SourceDate, string(in.Estado))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *Repository) MarkDownloaded(ctx context.Context, fileCodigo string) error {
	const q = `UPDATE file_dispatch SET estado=$2, updated_at=NOW() WHERE file_codigo=$1`
	_, err := r.db.ExecContext(ctx, q, fileCodigo, string(domain.StateDownloaded))
	return err
}

func (r *Repository) MarkSent(ctx context.Context, fileCodigo, sftpPath string) error {
	const q = `UPDATE file_dispatch SET estado=$2, sftp_path=$3, updated_at=NOW() WHERE file_codigo=$1`
	_, err := r.db.ExecContext(ctx, q, fileCodigo, string(domain.StateSent), sftpPath)
	if err != nil {
		return err
	}
	return r.UpsertMeta(ctx, "last_file_codigo", fileCodigo)
}

func (r *Repository) MarkError(ctx context.Context, fileCodigo string) error {
	const q = `UPDATE file_dispatch SET estado=$2, intentos=intentos+1, updated_at=NOW() WHERE file_codigo=$1`
	_, err := r.db.ExecContext(ctx, q, fileCodigo, string(domain.StateError))
	return err
}

func (r *Repository) MarkRetrying(ctx context.Context, fileCodigo string) error {
	const q = `UPDATE file_dispatch SET estado=$2, updated_at=NOW() WHERE file_codigo=$1`
	_, err := r.db.ExecContext(ctx, q, fileCodigo, string(domain.StateRetrying))
	return err
}

func (r *Repository) MarkFailed(ctx context.Context, fileCodigo string) error {
	const q = `UPDATE file_dispatch SET estado=$2, updated_at=NOW() WHERE file_codigo=$1`
	_, err := r.db.ExecContext(ctx, q, fileCodigo, string(domain.StateFailed))
	return err
}

func (r *Repository) EnqueueRetry(ctx context.Context, fileCodigo string, errMsg string, nextRetryAt time.Time) error {
	const q = `INSERT INTO retry_queue
	(file_codigo, estado, intentos, next_retry_at, ultimo_error, created_at, updated_at)
	VALUES ($1,$2,1,$3,$4,NOW(),NOW())`
	_, err := r.db.ExecContext(ctx, q, fileCodigo, string(domain.StatePending), nextRetryAt, errMsg)
	return err
}

func (r *Repository) AcquireRetryBatch(ctx context.Context, limit, maxAttempts int) ([]RetryRow, error) {
	if limit <= 0 {
		limit = 50
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	q := `SELECT id, file_codigo, estado, intentos, COALESCE(ultimo_error,'')
		FROM retry_queue
		WHERE estado IN ($1,$2)
		  AND intentos < $3
		  AND (next_retry_at IS NULL OR next_retry_at <= NOW())
		ORDER BY created_at
		LIMIT $4
		FOR UPDATE SKIP LOCKED`
	rows, err := tx.QueryContext(ctx, q, string(domain.StatePending), string(domain.StateRetrying), maxAttempts, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]RetryRow, 0, limit)
	for rows.Next() {
		var rr RetryRow
		var st string
		if err := rows.Scan(&rr.ID, &rr.FileCodigo, &st, &rr.Intentos, &rr.UltimoError); err != nil {
			return nil, err
		}
		rr.Estado = domain.FileState(st)
		out = append(out, rr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, rr := range out {
		if _, err := tx.ExecContext(ctx, `UPDATE retry_queue SET estado=$2, updated_at=NOW() WHERE id=$1`, rr.ID, string(domain.StateRetrying)); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repository) OnRetrySuccess(ctx context.Context, retryID int64, fileCodigo, sftpPath string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `UPDATE retry_queue SET estado=$2, updated_at=NOW() WHERE id=$1`, retryID, string(domain.StateSent)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE file_dispatch SET estado=$2, sftp_path=$3, updated_at=NOW() WHERE file_codigo=$1`, fileCodigo, string(domain.StateSent), sftpPath); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_metadata(key, value, updated_at) VALUES('last_file_codigo',$1,NOW()) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`, fileCodigo); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) OnRetryFailure(ctx context.Context, retryID int64, fileCodigo string, nextRetry time.Time, errMsg string, maxAttempts int) (becameFailed bool, err error) {
	const q = `UPDATE retry_queue
	SET intentos = intentos + 1,
	    estado = CASE WHEN intentos + 1 >= $2 THEN $5 ELSE $3 END,
	    next_retry_at = CASE WHEN intentos + 1 >= $2 THEN NULL ELSE $4::timestamp END,
	    ultimo_error = $6,
	    updated_at = NOW()
	WHERE id=$1
	RETURNING intentos, estado`
	var attempts int
	var estado string
	err = r.db.QueryRowContext(ctx, q, retryID, maxAttempts, string(domain.StatePending), nextRetry, string(domain.StateFailed), errMsg).Scan(&attempts, &estado)
	if err != nil {
		return false, err
	}

	if estado == string(domain.StateFailed) {
		if err := r.MarkFailed(ctx, fileCodigo); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := r.MarkError(ctx, fileCodigo); err != nil {
		return false, err
	}
	return false, nil
}

func (r *Repository) GetDispatchByCode(ctx context.Context, fileCodigo string) (*DispatchRecord, error) {
	const q = `SELECT file_codigo, nombre_archivo, ruta, estado, intentos FROM file_dispatch WHERE file_codigo=$1`
	var rec DispatchRecord
	var st string
	err := r.db.QueryRowContext(ctx, q, fileCodigo).Scan(&rec.FileCodigo, &rec.NombreArchivo, &rec.Ruta, &st, &rec.Intentos)
	if err != nil {
		return nil, err
	}
	rec.Estado = domain.FileState(st)
	return &rec, nil
}

func (r *Repository) GetLastSourceDate(ctx context.Context) (time.Time, bool, error) {
	const q = `SELECT MAX(source_date) FROM file_dispatch`
	var t sql.NullTime
	if err := r.db.QueryRowContext(ctx, q).Scan(&t); err != nil {
		return time.Time{}, false, err
	}
	if !t.Valid {
		return time.Time{}, false, nil
	}
	return t.Time, true, nil
}

func (r *Repository) GetMeta(ctx context.Context, key string) (string, bool, error) {
	const q = `SELECT value FROM app_metadata WHERE key=$1`
	var val string
	err := r.db.QueryRowContext(ctx, q, key).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

func (r *Repository) UpsertMeta(ctx context.Context, key, value string) error {
	const q = `INSERT INTO app_metadata(key, value, updated_at) VALUES($1,$2,NOW())
	ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`
	_, err := r.db.ExecContext(ctx, q, key, value)
	return err
}

func (r *Repository) ListSentOlderThan(ctx context.Context, before time.Time, limit int) ([]DispatchRecord, error) {
	if limit <= 0 {
		limit = 200
	}
	const q = `SELECT file_codigo, nombre_archivo, estado, intentos
	FROM file_dispatch
	WHERE estado=$1 AND updated_at < $2
	ORDER BY updated_at
	LIMIT $3`
	rows, err := r.db.QueryContext(ctx, q, string(domain.StateSent), before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]DispatchRecord, 0, limit)
	for rows.Next() {
		var rec DispatchRecord
		var st string
		if err := rows.Scan(&rec.FileCodigo, &rec.NombreArchivo, &st, &rec.Intentos); err != nil {
			return nil, err
		}
		rec.Estado = domain.FileState(st)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (r *Repository) TouchLock(ctx context.Context, fileCodigo string) error {
	const q = `UPDATE file_dispatch SET updated_at=NOW() WHERE file_codigo=$1`
	_, err := r.db.ExecContext(ctx, q, fileCodigo)
	return err
}

func (r *Repository) Stats(ctx context.Context) (string, error) {
	const q = `SELECT COUNT(*) FROM file_dispatch`
	var n int
	if err := r.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return "", err
	}
	return fmt.Sprintf("file_dispatch=%d", n), nil
}
