package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

var ErrNotFound = errors.New("订阅不存在")

// MaxContentBytes caps a single subscription document.
const MaxContentBytes = 2 << 20 // 2 MiB

// Subscription is one Clash config plus the token that serves it.
type Subscription struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Note      string    `json:"note"`
	Token     string    `json:"token"`
	Content   string    `json:"content,omitempty"`
	Enabled   bool      `json:"enabled"`
	Proxies   int       `json:"proxies"`
	Groups    int       `json:"groups"`
	Rules     int       `json:"rules"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// summary is the list view: same shape minus the document body, so listing a
// hundred subscriptions doesn't ship a hundred configs.
func (s *Subscription) summary() Subscription {
	c := *s
	c.Content = ""
	return c
}

type fileData struct {
	NextID int64           `json:"next_id"`
	Items  []*Subscription `json:"items"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	data fileData
}

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path}
	s.data.NextID = 1

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, s.persist()
		}
		return nil, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s.data); err != nil {
			return nil, err
		}
	}
	if s.data.NextID < 1 {
		s.data.NextID = 1
	}
	return s, nil
}

// persist writes through a temp file so a crash mid-write cannot truncate the
// only copy of the operator's configs. Caller holds the lock.
func (s *Store) persist() error {
	if dir := filepath.Dir(s.path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	body, err := json.MarshalIndent(&s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// counts parses a Clash document far enough to report what is in it. An
// unparseable document is an error; the API refuses to store one.
func counts(content string) (proxies, groups, rules int, err error) {
	var doc struct {
		Proxies     []yaml.Node `yaml:"proxies"`
		ProxyGroups []yaml.Node `yaml:"proxy-groups"`
		Rules       []yaml.Node `yaml:"rules"`
	}
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return 0, 0, 0, err
	}
	return len(doc.Proxies), len(doc.ProxyGroups), len(doc.Rules), nil
}

func (s *Store) List() []Subscription {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Subscription, 0, len(s.data.Items))
	for _, it := range s.data.Items {
		out = append(out, it.summary())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func (s *Store) Get(id int64) (Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, it := range s.data.Items {
		if it.ID == id {
			return *it, nil
		}
	}
	return Subscription{}, ErrNotFound
}

// ByToken returns the subscription a /subscribe request asked for. Disabled
// subscriptions are reported as missing so a paused token leaks nothing.
func (s *Store) ByToken(token string) (Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, it := range s.data.Items {
		if it.Token == token && it.Enabled {
			return *it, nil
		}
	}
	return Subscription{}, ErrNotFound
}

func (s *Store) Create(name, note, content string) (Subscription, error) {
	p, g, r, err := counts(content)
	if err != nil {
		return Subscription{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	it := &Subscription{
		ID:        s.data.NextID,
		Name:      name,
		Note:      note,
		Token:     randomHex(16),
		Content:   content,
		Enabled:   true,
		Proxies:   p,
		Groups:    g,
		Rules:     r,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.data.NextID++
	s.data.Items = append(s.data.Items, it)

	if err := s.persist(); err != nil {
		return Subscription{}, err
	}
	return *it, nil
}

func (s *Store) Update(id int64, name, note, content string) (Subscription, error) {
	p, g, r, err := counts(content)
	if err != nil {
		return Subscription{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, it := range s.data.Items {
		if it.ID != id {
			continue
		}
		it.Name, it.Note, it.Content = name, note, content
		it.Proxies, it.Groups, it.Rules = p, g, r
		it.UpdatedAt = time.Now()
		if err := s.persist(); err != nil {
			return Subscription{}, err
		}
		return *it, nil
	}
	return Subscription{}, ErrNotFound
}

func (s *Store) SetEnabled(id int64, enabled bool) (Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, it := range s.data.Items {
		if it.ID != id {
			continue
		}
		it.Enabled = enabled
		it.UpdatedAt = time.Now()
		if err := s.persist(); err != nil {
			return Subscription{}, err
		}
		return *it, nil
	}
	return Subscription{}, ErrNotFound
}

func (s *Store) RotateToken(id int64) (Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, it := range s.data.Items {
		if it.ID != id {
			continue
		}
		it.Token = randomHex(16)
		it.UpdatedAt = time.Now()
		if err := s.persist(); err != nil {
			return Subscription{}, err
		}
		return *it, nil
	}
	return Subscription{}, ErrNotFound
}

func (s *Store) Delete(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, it := range s.data.Items {
		if it.ID == id {
			s.data.Items = append(s.data.Items[:i], s.data.Items[i+1:]...)
			return s.persist()
		}
	}
	return ErrNotFound
}
