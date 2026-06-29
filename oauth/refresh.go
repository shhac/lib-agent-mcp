package oauth

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
	grants jsonMapStore[refreshGrant]
}

func newRefreshStore(store SecretStore) *refreshStore {
	return &refreshStore{grants: jsonMapStore[refreshGrant]{store: store, key: refreshStoreKey}}
}

// issue stores a fresh refresh token for clientID/scope and returns it.
func (s *refreshStore) issue(clientID, scope string) (string, error) {
	token, err := randToken(32)
	if err != nil {
		return "", err
	}
	if err := s.grants.mutate(func(m map[string]refreshGrant) bool {
		m[token] = refreshGrant{ClientID: clientID, Scope: scope}
		return true
	}); err != nil {
		return "", err
	}
	return token, nil
}

// exchange consumes token (rotation): it returns the grant and removes the token,
// reporting false if it is unknown.
func (s *refreshStore) exchange(token string) (refreshGrant, bool, error) {
	var g refreshGrant
	var found bool
	err := s.grants.mutate(func(m map[string]refreshGrant) bool {
		if g, found = m[token]; found {
			delete(m, token)
		}
		return found
	})
	if err != nil {
		return refreshGrant{}, false, err
	}
	return g, found, nil
}
