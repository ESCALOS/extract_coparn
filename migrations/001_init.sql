CREATE TABLE IF NOT EXISTS file_dispatch (
  id SERIAL PRIMARY KEY,
  file_codigo VARCHAR UNIQUE NOT NULL,
  nombre_archivo VARCHAR NOT NULL,
  ruta TEXT NOT NULL,
  source_date TIMESTAMP,
  estado VARCHAR NOT NULL,
  intentos INT DEFAULT 0,
  sftp_path TEXT,
  checksum TEXT,
  locked_at TIMESTAMP,
  created_at TIMESTAMP DEFAULT NOW(),
  updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS retry_queue (
  id SERIAL PRIMARY KEY,
  file_codigo VARCHAR NOT NULL,
  estado VARCHAR NOT NULL,
  intentos INT DEFAULT 0,
  next_retry_at TIMESTAMP,
  ultimo_error TEXT,
  created_at TIMESTAMP DEFAULT NOW(),
  updated_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_retry_pending
ON retry_queue (estado, next_retry_at);

CREATE TABLE IF NOT EXISTS app_metadata (
  key VARCHAR PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TIMESTAMP DEFAULT NOW()
);
