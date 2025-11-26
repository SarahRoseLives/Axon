package discovery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	mrand "math/rand" // Using math/rand for initialization
	"sync"
	"time"
)

type Peer struct {
	OnionAddress string    `json:"onion_address"`
	Nickname     string    `json:"nickname"`
	IntroducedBy string    `json:"introduced_by"`
	TrustLevel   string    `json:"trust_level"`
	LastSeen     time.Time `json:"last_seen"`
	PublicKey    string    `json:"public_key"`
	Status       string    `json:"status"`
	Blocked      bool      `json:"blocked"`
	// NEW: Fixed position for stable map rendering
	XPos         float64   `json:"x_pos"`
	YPos         float64   `json:"y_pos"`
}

type PeerManager struct {
	mu           sync.RWMutex
	KnownPeers map[string]Peer
	DataDir      string
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

func (pm *PeerManager) IsBlocked(onion string) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if p, ok := pm.KnownPeers[onion]; ok {
		return p.Blocked
	}
	return false
}

func (pm *PeerManager) GetPublicKey(onion string) string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if p, ok := pm.KnownPeers[onion]; ok {
		return p.PublicKey
	}
	return ""
}

// --- MANAGEMENT ACTIONS ---

func (pm *PeerManager) RemovePeer(onion string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	delete(pm.KnownPeers, onion)
	pm.savePeersInternal()
	fmt.Printf("🗑️ Removed peer: %s\n", onion)
}

func (pm *PeerManager) ToggleBlock(onion string) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	p, exists := pm.KnownPeers[onion]
	if !exists { return false }

	p.Blocked = !p.Blocked
	pm.KnownPeers[onion] = p
	pm.savePeersInternal()

	status := "Blocked"
	if !p.Blocked { status = "Unblocked" }
	fmt.Printf("🚫 %s peer: %s\n", status, onion)

	return p.Blocked
}

// --- STANDARD LOGIC ---

// Updated: Now accepts 'introducedBy'
func (pm *PeerManager) AddPeer(onion, trust, pubKey, nickname, introducedBy string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	p, exists := pm.KnownPeers[onion]
	if !exists {
		p = Peer{
			OnionAddress: onion,
			TrustLevel:   trust,
			Status:       "unknown",
			Blocked:      false,
			XPos:         -1, // Initialize to -1 to signify "not placed yet"
			YPos:         -1,
		}
	}

	if p.Blocked { return }

	p.LastSeen = time.Now()

	if pubKey != "" { p.PublicKey = pubKey }
	if nickname != "" { p.Nickname = nickname }

	// Only set IntroducedBy if it's new (don't overwrite existing history)
	if introducedBy != "" && p.IntroducedBy == "" {
		p.IntroducedBy = introducedBy
	}

	if p.TrustLevel == "unknown" && trust != "unknown" {
		p.TrustLevel = trust
	}

	// NEW: Assign a stable angular position on first appearance
	if p.XPos == -1 {
		angle := mrand.Float64() * 2 * 3.14159265359 // Random angle [0, 2*PI]
		p.XPos = angle
		// We use YPos to store a random radius factor [0.8, 1.0]
		// to prevent perfect stacking, making the ring look more natural.
		p.YPos = mrand.Float64()*0.2 + 0.8
	}


	pm.KnownPeers[onion] = p
	pm.savePeersInternal()

	// Log if new
	if !exists {
		source := "Manually"
		if p.IntroducedBy != "" { source = "via " + p.IntroducedBy }
		fmt.Printf("🔭 Peer Added: %s (%s)\n", onion, source)
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