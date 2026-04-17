package alerts

import (
	"fmt"
	"log"
	"sync"

	"extract_coparn/internal/notifier"
)

type Monitor struct {
	n notifier.Notifier

	mu    sync.Mutex
	state map[string]bool // true=UP, false=DOWN
}

func NewMonitor(n notifier.Notifier) *Monitor {
	return &Monitor{
		n:     n,
		state: map[string]bool{"API": true, "SFTP": true},
	}
}

func (m *Monitor) ServiceOK(service string) {
	m.mu.Lock()
	prev, ok := m.state[service]
	if !ok {
		prev = true
	}
	if prev {
		m.state[service] = true
		m.mu.Unlock()
		return
	}
	m.state[service] = true
	m.mu.Unlock()

	subj := fmt.Sprintf("[RECOVERY] Servicio %s restablecido", service)
	body := fmt.Sprintf("El servicio %s cambió de DOWN a UP.", service)
	if err := m.n.Send(subj, body); err != nil {
		log.Printf("alert recovery error: %v", err)
	}
}

func (m *Monitor) ServiceDown(service string, reason error) {
	m.mu.Lock()
	prev, ok := m.state[service]
	if !ok {
		prev = true
	}
	if !prev {
		m.state[service] = false
		m.mu.Unlock()
		return
	}
	m.state[service] = false
	m.mu.Unlock()

	subj := fmt.Sprintf("[ALERT] Servicio %s caído", service)
	body := fmt.Sprintf("El servicio %s cambió de UP a DOWN. Error: %v", service, reason)
	if err := m.n.Send(subj, body); err != nil {
		log.Printf("alert down error: %v", err)
	}
}

func (m *Monitor) FileFailed(fileCodigo string, reason error) {
	subj := fmt.Sprintf("[FAILED] Archivo %s", fileCodigo)
	body := fmt.Sprintf("El archivo %s llegó a estado FAILED. Último error: %v", fileCodigo, reason)
	if err := m.n.Send(subj, body); err != nil {
		log.Printf("alert failed error: %v", err)
	}
}
