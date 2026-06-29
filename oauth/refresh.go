package oauth

import (
	"encoding/json"
	"sync"
)

// refreshStoreKey holds the JSON map of refresh tokens in the SecretStore. They
// persist so a client stays connected across server restarts.
const refreshStoreKey = "refresh-tokens"

// refreshGrant is what a refresh token stands for: the client and scope a new
// access token should be minted with.
type refreshGrant struct {
	ClientID string `json:"client_id"`
	Scope    string `json:"scope"`
}

// refreshStore issues and exchanges refresh tokens, rotating them on use (the
// exchanged token is invalidated and a new one issued), persisted via SecretStore.
type refreshStore struct {
	store SecretStore
	mu    sync.Mutex
}

func newRefreshStore(store SecretStore) *refreshStore {
	return &refreshStore{store: store}
}

// issue stores a fresh refresh token for clientID/scope and returns it.
func (s *refreshStore) issue(clientID, scope string) (string, error) {
	token, err := randToken(32)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return "", err
	}
	m[token] = refreshGrant{ClientID: clientID, Scope: scope}
	if err := s.save(m); err != nil {
		return "", err
	}
	return token, nil
}

// exchange consumes token (rotation): it returns the grant and removes the token,
// reporting false if it is unknown.
func (s *refreshStore) exchange(token string) (refreshGrant, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return refreshGrant{}, false, err
	}
	g, ok := m[token]
	if !ok {
		return refreshGrant{}, false, nil
	}
	delete(m, token)
	if err := s.save(m); err != nil {
		return refreshGrant{}, false, err
	}
	return g, true, nil
}

func (s *refreshStore) load() (map[string]refreshGrant, error) {
	v, ok, err := s.store.Get(refreshStoreKey)
	if err != nil {
		return nil, err
	}
	if !ok || v == "" {
		return map[string]refreshGrant{}, nil
	}
	var m map[string]refreshGrant
	if err := json.Unmarshal([]byte(v), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *refreshStore) save(m map[string]refreshGrant) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return s.store.Set(refreshStoreKey, string(b))
}
