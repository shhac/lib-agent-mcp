package oauth

import (
	"encoding/json"
	"sync"
)

// clientsStoreKey holds the JSON map of registered clients in the SecretStore.
const clientsStoreKey = "clients"

// Client is a dynamically-registered OAuth client (RFC 7591). Clients are public
// (PKCE, no secret): Claude and other harnesses each register themselves to get
// an id before the authorize step.
type Client struct {
	ID           string   `json:"client_id"`
	RedirectURIs []string `json:"redirect_uris"`
	Name         string   `json:"client_name,omitempty"`
}

// allowsRedirect reports whether uri exactly matches a registered redirect URI —
// the check that stops an attacker from redirecting an auth code elsewhere.
func (c Client) allowsRedirect(uri string) bool {
	for _, r := range c.RedirectURIs {
		if r == uri {
			return true
		}
	}
	return false
}

// clientRegistry persists dynamically-registered clients via the SecretStore, so
// a client stays registered across restarts. The whole map is stored under one
// key; a mutex serializes the load-modify-save.
type clientRegistry struct {
	store SecretStore
	mu    sync.Mutex
}

func newClientRegistry(store SecretStore) *clientRegistry {
	return &clientRegistry{store: store}
}

// Register creates a client with a fresh id for the given redirect URIs and name.
func (r *clientRegistry) Register(redirectURIs []string, name string) (Client, error) {
	id, err := randToken(16)
	if err != nil {
		return Client{}, err
	}
	c := Client{ID: id, RedirectURIs: redirectURIs, Name: name}

	r.mu.Lock()
	defer r.mu.Unlock()
	m, err := r.load()
	if err != nil {
		return Client{}, err
	}
	m[id] = c
	if err := r.save(m); err != nil {
		return Client{}, err
	}
	return c, nil
}

// Get returns the client for id and whether it is registered.
func (r *clientRegistry) Get(id string) (Client, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, err := r.load()
	if err != nil {
		return Client{}, false, err
	}
	c, ok := m[id]
	return c, ok, nil
}

func (r *clientRegistry) load() (map[string]Client, error) {
	v, ok, err := r.store.Get(clientsStoreKey)
	if err != nil {
		return nil, err
	}
	if !ok || v == "" {
		return map[string]Client{}, nil
	}
	var m map[string]Client
	if err := json.Unmarshal([]byte(v), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (r *clientRegistry) save(m map[string]Client) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return r.store.Set(clientsStoreKey, string(b))
}
