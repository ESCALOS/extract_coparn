package domain

type FileState string

const (
	StatePending    FileState = "PENDING"
	StateDownloaded FileState = "DOWNLOADED"
	StateSent       FileState = "SENT"
	StateError      FileState = "ERROR"
	StateRetrying   FileState = "RETRYING"
	StateFailed     FileState = "FAILED"
)
