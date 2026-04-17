package domain

import "time"

type LoginRequest struct {
	Username  string `json:"username"`
	Password  string `json:"password"`
	TipoLogin string `json:"tipoLogin"`
}

type LoginResponse struct {
	AccessToken string `json:"accessToken"`
	ExpiresInMs int64  `json:"expiresInMs"`
	Codigo      string `json:"codigo"`
}

type ListResponse struct {
	Codigo int       `json:"codigo"`
	Data   []RawFile `json:"data"`
}

type RawFile struct {
	FileCodigo    string `json:"CHR_FILECODIGO"`
	NombreArchivo string `json:"NOMBRE_ARCHIVO"`
	Ruta          string `json:"RUTA"`
	FechaCreado   string `json:"FECHA_CREADO"`
}

func (r RawFile) SourceDate() *time.Time {
	if r.FechaCreado == "" {
		return nil
	}
	formats := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"}
	for _, f := range formats {
		t, err := time.Parse(f, r.FechaCreado)
		if err == nil {
			return &t
		}
	}
	return nil
}

type RetryRow struct {
	ID          int64
	FileCodigo  string
	Estado      string
	Intentos    int
	UltimoError string
}
