package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"extract_coparn/internal/config"
	"extract_coparn/internal/domain"
)

type APIClient struct {
	cfg        config.APIConfig
	httpClient *http.Client

	mu        sync.Mutex
	token     string
	codigo    string
	expiresAt time.Time
}

func NewAPIClient(cfg config.APIConfig) *APIClient {
	return &APIClient{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
	}
}

func (c *APIClient) EnsureToken(ctx context.Context) (token string, codigo string, err error) {
	c.mu.Lock()
	if c.token != "" && time.Now().Add(c.cfg.TokenSkew).Before(c.expiresAt) {
		t, code := c.token, c.codigo
		c.mu.Unlock()
		return t, code, nil
	}
	c.mu.Unlock()

	reqBody := domain.LoginRequest{
		Username:  c.cfg.BodyUsername,
		Password:  c.cfg.BodyPassword,
		TipoLogin: c.cfg.TipoLogin,
	}
	body, _ := json.Marshal(reqBody)

	u, err := joinURL(c.cfg.BaseURL, c.cfg.AuthPath)
	if err != nil {
		return "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.cfg.BasicUsername, c.cfg.BasicPassword)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("login status %d: %s", resp.StatusCode, string(raw))
	}

	var out domain.LoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", err
	}
	if out.AccessToken == "" || out.Codigo == "" {
		return "", "", fmt.Errorf("respuesta login inválida")
	}

	c.mu.Lock()
	c.token = out.AccessToken
	c.codigo = out.Codigo
	expiresIn := time.Duration(out.ExpiresInMs) * time.Millisecond
	if expiresIn <= 0 {
		expiresIn = time.Hour
	}
	c.expiresAt = time.Now().Add(expiresIn)
	t, code := c.token, c.codigo
	c.mu.Unlock()

	return t, code, nil
}

func (c *APIClient) ListFiles(ctx context.Context, token, codigo string, from, to time.Time) ([]domain.RawFile, error) {
	u, err := joinURL(c.cfg.BaseURL, c.cfg.ListPath)
	if err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(u)
	q := parsed.Query()
	q.Set("usuaCodigo", codigo)
	q.Set("procesado", "0")
	q.Set("fechaDesde", from.Format("2006-01-02"))
	q.Set("fechaHasta", to.Format("2006-01-02"))
	parsed.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("listado status %d: %s", resp.StatusCode, string(raw))
	}

	var out domain.ListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (c *APIClient) GetSignedURL(ctx context.Context, token, ruta, fileName string) (string, error) {
	u, err := joinURL(c.cfg.BaseURL, c.cfg.SignedURLPath)
	if err != nil {
		return "", err
	}
	parsed, _ := url.Parse(u)
	q := parsed.Query()
	if !strings.HasSuffix(ruta, "/") {
		ruta += "/"
	}
	q.Set("path", ruta)
	q.Set("fileName", fileName)
	parsed.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("signed-url status %d: %s", resp.StatusCode, string(raw))
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	asText := strings.TrimSpace(string(raw))
	if strings.HasPrefix(asText, "http://") || strings.HasPrefix(asText, "https://") {
		return asText, nil
	}

	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		if v, ok := obj["data"].(string); ok && v != "" {
			return v, nil
		}
		if v, ok := obj["url"].(string); ok && v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("respuesta signed-url no reconocida")
}

func joinURL(base, p string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = path.Join(u.Path, p)
	if strings.HasSuffix(p, "/") && !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return u.String(), nil
}
