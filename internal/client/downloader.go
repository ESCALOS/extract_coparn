package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type DownloadResult struct {
	Path     string
	Checksum string
}

func DownloadFile(ctx context.Context, signedURL, dataDir, fileName string, timeout time.Duration) (*DownloadResult, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	dst := filepath.Join(dataDir, fileName)

	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download status %d", resp.StatusCode)
	}

	f, err := os.Create(dst)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	h := sha256.New()
	mw := io.MultiWriter(f, h)
	if _, err := io.Copy(mw, resp.Body); err != nil {
		return nil, err
	}
	return &DownloadResult{Path: dst, Checksum: hex.EncodeToString(h.Sum(nil))}, nil
}
