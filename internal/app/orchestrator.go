package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"extract_coparn/internal/alerts"
	"extract_coparn/internal/client"
	"extract_coparn/internal/config"
	"extract_coparn/internal/domain"
	"extract_coparn/internal/repo"
	"extract_coparn/internal/retry"
)

type Orchestrator struct {
	cfg    *config.Config
	api    *client.APIClient
	sftp   *client.SFTPClient
	repo   *repo.Repository
	alerts *alerts.Monitor
}

func NewOrchestrator(cfg *config.Config, api *client.APIClient, sftp *client.SFTPClient, repository *repo.Repository, alerts *alerts.Monitor) *Orchestrator {
	return &Orchestrator{cfg: cfg, api: api, sftp: sftp, repo: repository, alerts: alerts}
}

func (o *Orchestrator) Run(ctx context.Context) error {
	if err := o.prepareDataDir(); err != nil {
		return err
	}
	go o.runRetryWorker(ctx)
	go o.runRetentionWorker(ctx)

	for {
		if err := o.runCycle(ctx); err != nil {
			log.Printf("cycle error: %v", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(o.cfg.App.LoopInterval):
		}
	}
}

func (o *Orchestrator) prepareDataDir() error {
	if err := os.MkdirAll(o.cfg.App.DataDir, 0o755); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrPermission) {
		return err
	}

	fallback := "./data"
	if err := os.MkdirAll(fallback, 0o755); err != nil {
		return fmt.Errorf("no se pudo crear APP_DATA_DIR (%s) ni fallback (%s): %w", o.cfg.App.DataDir, fallback, err)
	}
	log.Printf("sin permisos para %s, usando fallback local %s", o.cfg.App.DataDir, fallback)
	o.cfg.App.DataDir = fallback
	return nil
}

func (o *Orchestrator) runCycle(ctx context.Context) error {
	token, codigo, err := o.api.EnsureToken(ctx)
	if err != nil {
		o.alerts.ServiceDown("API", err)
		return err
	}
	o.alerts.ServiceOK("API")

	from, to, err := o.resolveRange(ctx)
	if err != nil {
		return err
	}
	files, err := o.api.ListFiles(ctx, token, codigo, from, to)
	if err != nil {
		o.alerts.ServiceDown("API", err)
		return err
	}
	o.alerts.ServiceOK("API")

	lastFileCodigo, _, _ := o.repo.GetMeta(ctx, "last_file_codigo")

	jobs := make(chan domain.RawFile)
	wg := sync.WaitGroup{}
	for i := 0; i < o.cfg.App.WorkerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range jobs {
				o.processFile(ctx, token, file, lastFileCodigo)
			}
		}()
	}

	for _, f := range files {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		case jobs <- f:
		}
	}
	close(jobs)
	wg.Wait()
	return nil
}

func (o *Orchestrator) resolveRange(ctx context.Context) (time.Time, time.Time, error) {
	to := time.Now()
	last, ok, err := o.repo.GetLastSourceDate(ctx)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !ok {
		from := to.AddDate(0, 0, -o.cfg.App.OverlapDays)
		return from, to, nil
	}
	from := last.AddDate(0, 0, -o.cfg.App.OverlapDays)
	return from, to, nil
}

func (o *Orchestrator) processFile(ctx context.Context, token string, f domain.RawFile, lastFileCodigo string) {
	if f.FileCodigo == "" || f.NombreArchivo == "" || f.Ruta == "" {
		return
	}

	if lastFileCodigo != "" && f.FileCodigo <= lastFileCodigo {
		exists, err := o.repo.FileExists(ctx, f.FileCodigo)
		if err == nil && exists {
			return
		}
	}

	exists, err := o.repo.FileExists(ctx, f.FileCodigo)
	if err != nil {
		log.Printf("exists error %s: %v", f.FileCodigo, err)
		return
	}
	if exists {
		return
	}

	inserted, err := o.repo.InsertPending(ctx, repo.FileDispatchInput{
		FileCodigo:    f.FileCodigo,
		NombreArchivo: f.NombreArchivo,
		Ruta:          f.Ruta,
		SourceDate:    f.SourceDate(),
		Estado:        domain.StatePending,
	})
	if err != nil || !inserted {
		if err != nil {
			log.Printf("insert pending error %s: %v", f.FileCodigo, err)
		}
		return
	}

	signedURL, err := o.api.GetSignedURL(ctx, token, f.Ruta, f.NombreArchivo)
	if err != nil {
		o.failAndQueue(ctx, f.FileCodigo, fmt.Errorf("signed-url: %w", err), 1)
		o.alerts.ServiceDown("API", err)
		return
	}
	o.alerts.ServiceOK("API")

	dl, err := client.DownloadFile(ctx, signedURL, o.cfg.App.DataDir, f.NombreArchivo, o.cfg.API.Timeout)
	if err != nil {
		o.failAndQueue(ctx, f.FileCodigo, fmt.Errorf("download: %w", err), 1)
		return
	}
	if err := o.repo.MarkDownloaded(ctx, f.FileCodigo); err != nil {
		log.Printf("mark downloaded error %s: %v", f.FileCodigo, err)
	}

	sftpPath, err := o.sftp.UploadFile(ctx, dl.Path, f.NombreArchivo)
	if err != nil {
		o.alerts.ServiceDown("SFTP", err)
		o.failAndQueue(ctx, f.FileCodigo, fmt.Errorf("sftp upload: %w", err), 1)
		return
	}
	o.alerts.ServiceOK("SFTP")

	if err := o.repo.MarkSent(ctx, f.FileCodigo, sftpPath); err != nil {
		log.Printf("mark sent error %s: %v", f.FileCodigo, err)
	}
}

func (o *Orchestrator) failAndQueue(ctx context.Context, fileCodigo string, reason error, nextAttempt int) {
	if err := o.repo.MarkError(ctx, fileCodigo); err != nil {
		log.Printf("mark error %s: %v", fileCodigo, err)
	}
	delay := retry.NextDelay(nextAttempt, o.cfg.Retry.JitterPct)
	if err := o.repo.EnqueueRetry(ctx, fileCodigo, reason.Error(), time.Now().Add(delay)); err != nil {
		log.Printf("enqueue retry %s: %v", fileCodigo, err)
	}
}

func (o *Orchestrator) runRetryWorker(ctx context.Context) {
	ticker := time.NewTicker(o.cfg.App.RetryWorkerEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := o.consumeRetryBatch(ctx); err != nil {
				log.Printf("retry worker error: %v", err)
			}
		}
	}
}

func (o *Orchestrator) consumeRetryBatch(ctx context.Context) error {
	batch, err := o.repo.AcquireRetryBatch(ctx, o.cfg.App.RetryBatchSize, o.cfg.Retry.MaxAttempts)
	if err != nil {
		return err
	}
	if len(batch) == 0 {
		return nil
	}

	jobs := make(chan repo.RetryRow)
	wg := sync.WaitGroup{}
	workers := o.cfg.App.RetryWorkerConcurrency
	if workers <= 0 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				o.processRetryItem(ctx, item)
			}
		}()
	}
	for _, rr := range batch {
		jobs <- rr
	}
	close(jobs)
	wg.Wait()
	return nil
}

func (o *Orchestrator) processRetryItem(ctx context.Context, rr repo.RetryRow) {
	rec, err := o.repo.GetDispatchByCode(ctx, rr.FileCodigo)
	if err != nil {
		log.Printf("retry get dispatch %s: %v", rr.FileCodigo, err)
		return
	}
	localPath := filepath.Join(o.cfg.App.DataDir, rec.NombreArchivo)

	sftpPath, err := o.sftp.UploadFile(ctx, localPath, rec.NombreArchivo)
	if err == nil {
		o.alerts.ServiceOK("SFTP")
		if err := o.repo.OnRetrySuccess(ctx, rr.ID, rr.FileCodigo, sftpPath); err != nil {
			log.Printf("retry success update %s: %v", rr.FileCodigo, err)
			return
		}
		if rmErr := os.Remove(localPath); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Printf("remove local file %s: %v", localPath, rmErr)
		}
		return
	}

	o.alerts.ServiceDown("SFTP", err)
	nextAttempt := rr.Intentos + 1
	delay := retry.NextDelay(nextAttempt, o.cfg.Retry.JitterPct)
	becameFailed, upErr := o.repo.OnRetryFailure(ctx, rr.ID, rr.FileCodigo, time.Now().Add(delay), err.Error(), o.cfg.Retry.MaxAttempts)
	if upErr != nil {
		log.Printf("retry failure update %s: %v", rr.FileCodigo, upErr)
		return
	}
	if becameFailed {
		o.alerts.FileFailed(rr.FileCodigo, err)
	}
}

func (o *Orchestrator) runRetentionWorker(ctx context.Context) {
	ticker := time.NewTicker(o.cfg.App.RetentionCleanupEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.cleanupSentFiles(ctx)
		}
	}
}

func (o *Orchestrator) cleanupSentFiles(ctx context.Context) {
	if o.cfg.App.RetentionSentDays <= 0 {
		return
	}
	before := time.Now().AddDate(0, 0, -o.cfg.App.RetentionSentDays)
	rows, err := o.repo.ListSentOlderThan(ctx, before, 500)
	if err != nil {
		log.Printf("cleanup query: %v", err)
		return
	}
	for _, r := range rows {
		p := filepath.Join(o.cfg.App.DataDir, r.NombreArchivo)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			log.Printf("cleanup remove %s: %v", p, err)
		}
	}
}
