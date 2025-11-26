package discovery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Peer represents a node in the network
type Peer struct {
	OnionAddress string    `json:"onion_address"`
	TrustLevel   string    `json:"trust_level"` // "direct" or "transitive"
	LastSeen     time.Time `json:"last_seen"`
	Status       string    `json:"status"` // "online", "offline"
}

// PeerManager handles the trust graph
type PeerManager struct {
	mu         sync.RWMutex
	KnownPeers map[string]Peer
	DataDir    string
}

func NewPeerManager(dataDir string) *PeerManager {
	pm := &PeerManager{
		KnownPeers: make(map[string]Peer),
		DataDir:    dataDir,
	}
	pm.LoadPeers()
	return pm
}

// AddPeer adds a trusted contact
func (pm *PeerManager) AddPeer(onion string, trust string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// If peer exists, update; otherwise create new
	if p, exists := pm.KnownPeers[onion]; exists {
		p.LastSeen = time.Now()
		pm.KnownPeers[onion] = p
	} else {
		pm.KnownPeers[onion] = Peer{
			OnionAddress: onion,
			TrustLevel:   trust,
			LastSeen:     time.Now(),
			Status:       "unknown", // We haven't dialed them yet
		}
	}
	pm.SavePeers()
	fmt.Printf("🔭 Peer Added/Updated: %s\n", onion)
}

func (pm *PeerManager) GetPeers() []Peer {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	peers := []Peer{}
	for _, p := range pm.KnownPeers {
		peers = append(peers, p)
	}
	return peers
}

// Persistence logic
func (pm *PeerManager) LoadPeers() {
	path := filepath.Join(pm.DataDir, "peers.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return
	}

	data, err := os.ReadFile(path)
	if err == nil {
		var loaded map[string]Peer
		if err := json.Unmarshal(data, &loaded); err == nil {
			pm.KnownPeers = loaded
			fmt.Printf("📖 Loaded %d peers from disk.\n", len(loaded))
		}
	}
}

func (pm *PeerManager) SavePeers() {
	path := filepath.Join(pm.DataDir, "peers.json")
	data, _ := json.MarshalIndent(pm.KnownPeers, "", "  ")
	os.WriteFile(path, data, 0600)
}