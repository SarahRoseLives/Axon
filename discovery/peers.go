package discovery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Peer struct {
	OnionAddress string    `json:"onion_address"`
	TrustLevel   string    `json:"trust_level"`
	LastSeen     time.Time `json:"last_seen"`
	PublicKey    string    `json:"public_key"` // Hex Encoded X25519 Key
	Status       string    `json:"status"`     // <--- Added back to fix the error
}

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

func (pm *PeerManager) HasPeer(onion string) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	_, exists := pm.KnownPeers[onion]
	return exists
}

func (pm *PeerManager) GetPublicKey(onion string) string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if p, ok := pm.KnownPeers[onion]; ok {
		return p.PublicKey
	}
	return ""
}

// AddPeer adds or updates a peer in the list
func (pm *PeerManager) AddPeer(onion, trust, pubKey string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	p, exists := pm.KnownPeers[onion]
	if !exists {
		p = Peer{
			OnionAddress: onion,
			TrustLevel:   trust,
			Status:       "unknown", // This line caused the error, but now it is valid
		}
	}

	p.LastSeen = time.Now()

	// Update Key if provided
	if pubKey != "" {
		p.PublicKey = pubKey
	}

	// Upgrade trust from unknown to direct/transitive
	if p.TrustLevel == "unknown" && trust != "unknown" {
		p.TrustLevel = trust
	}

	pm.KnownPeers[onion] = p
	pm.savePeersInternal()

	if !exists || (pubKey != "" && pubKey != p.PublicKey) {
		fmt.Printf("🔭 Peer Updated: %s (Key: %v)\n", onion, pubKey != "")
	}
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

// Persistence
func (pm *PeerManager) LoadPeers() {
	path := filepath.Join(pm.DataDir, "peers.json")
	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, &pm.KnownPeers)
		fmt.Printf("📖 Loaded %d peers from disk.\n", len(pm.KnownPeers))
	}
}

func (pm *PeerManager) savePeersInternal() {
	path := filepath.Join(pm.DataDir, "peers.json")
	data, _ := json.MarshalIndent(pm.KnownPeers, "", "  ")
	os.WriteFile(path, data, 0600)
}