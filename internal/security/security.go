package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Peer struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Token    string `json:"token"`
	PairedAt int64  `json:"paired_at"`
}

type Credential struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Token    string `json:"token"`
	PairedAt int64  `json:"paired_at"`
}

type state struct {
	DeviceID    string                `json:"device_id"`
	Trusted     map[string]Peer       `json:"trusted"`
	Credentials map[string]Credential `json:"credentials"`
}

type Manager struct {
	mu       sync.RWMutex
	path     string
	state    state
	pairCode string
}

func New(path string) (*Manager, error) {
	m := &Manager{path: path, pairCode: newPairCode()}
	m.state.Trusted = map[string]Peer{}
	m.state.Credentials = map[string]Credential{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &m.state); err != nil {
			return nil, fmt.Errorf("read security state: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if m.state.Trusted == nil {
		m.state.Trusted = map[string]Peer{}
	}
	if m.state.Credentials == nil {
		m.state.Credentials = map[string]Credential{}
	}
	if strings.TrimSpace(m.state.DeviceID) == "" {
		m.state.DeviceID = randomToken(18)
		if err := m.saveLocked(); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Manager) DeviceID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.DeviceID
}

func (m *Manager) PairCode() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pairCode
}

func (m *Manager) RegeneratePairCode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pairCode = newPairCode()
	return m.pairCode
}

func (m *Manager) CheckPairCode(code string) bool {
	m.mu.RLock()
	expected := m.pairCode
	m.mu.RUnlock()
	if len(code) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(code), []byte(expected)) == 1
}

func (m *Manager) Trust(id, name string) (Peer, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Peer{}, errors.New("missing device id")
	}
	p := Peer{ID: id, Name: strings.TrimSpace(name), Token: randomToken(32), PairedAt: time.Now().Unix()}
	m.mu.Lock()
	m.state.Trusted[id] = p
	err := m.saveLocked()
	m.mu.Unlock()
	return p, err
}

func (m *Manager) SaveCredential(id, name, token string) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(token) == "" {
		return errors.New("invalid credential")
	}
	m.mu.Lock()
	m.state.Credentials[id] = Credential{ID: id, Name: strings.TrimSpace(name), Token: token, PairedAt: time.Now().Unix()}
	err := m.saveLocked()
	m.mu.Unlock()
	return err
}

func (m *Manager) Credential(id string) (Credential, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.state.Credentials[id]
	return c, ok
}

func (m *Manager) VerifyToken(token string) (Peer, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Peer{}, false
	}
	want := sha256.Sum256([]byte(token))
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.state.Trusted {
		got := sha256.Sum256([]byte(p.Token))
		if subtle.ConstantTimeCompare(want[:], got[:]) == 1 {
			return p, true
		}
	}
	return Peer{}, false
}

func (m *Manager) Trusted() []Peer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Peer, 0, len(m.state.Trusted))
	for _, p := range m.state.Trusted {
		p.Token = ""
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PairedAt > out[j].PairedAt })
	return out
}

func (m *Manager) IsPaired(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.state.Credentials[id]
	return ok
}

func (m *Manager) RevokeTrusted(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.state.Trusted, id)
	return m.saveLocked()
}

func (m *Manager) RevokeCredential(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.state.Credentials, id)
	return m.saveLocked()
}

func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(m.path, b, 0600)
}

func KeyFromToken(token string) [32]byte { return sha256.Sum256([]byte(token)) }

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func newPairCode() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	}
	n := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	return fmt.Sprintf("%06d", n%1000000)
}
