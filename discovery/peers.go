package discovery

import (
    "axon/database"
    "database/sql"
    "fmt"
    "time"
)

type Peer struct {
    OnionAddress string    `json:"onion_address"`
    Nickname     string    `json:"nickname"`
    IntroducedBy string    `json:"introduced_by"`
    TrustLevel   string    `json:"trust_level"`
    LastSeen     time.Time `json:"last_seen"`
    PublicKey    string    `json:"public_key"`
    Blocked      bool      `json:"blocked"`
    // XPos/YPos removed as they are now handled by JS Physics engine
}

type PeerManager struct {
    // No Mutex needed for DB access
}

func NewPeerManager(dataDir string) *PeerManager {
    return &PeerManager{}
}

// --- CORE METHODS ---

func (pm *PeerManager) HasPeer(onion string) bool {
    var exists bool
    // SQLite specific syntax for efficient boolean check
    query := "SELECT EXISTS(SELECT 1 FROM peers WHERE onion_address = ?)"
    err := database.DB.QueryRow(query, onion).Scan(&exists)
    if err != nil {
        return false
    }
    return exists
}

func (pm *PeerManager) AddPeer(onion, trust, pubKey, nickname, introducedBy string) {
    // Upsert Logic (Insert or Update)
    query := `
    INSERT INTO peers (onion_address, trust_level, public_key, nickname, introduced_by, last_seen, is_blocked)
    VALUES (?, ?, ?, ?, ?, ?, 0)
    ON CONFLICT(onion_address) DO UPDATE SET
        last_seen = excluded.last_seen,
        nickname = COALESCE(NULLIF(excluded.nickname, ''), nickname),
        public_key = COALESCE(NULLIF(excluded.public_key, ''), public_key);
    `
    _, err := database.DB.Exec(query, onion, trust, pubKey, nickname, introducedBy, time.Now())
    if err != nil {
        fmt.Printf("❌ DB Error adding peer: %v\n", err)
    }
}

func (pm *PeerManager) GetPeers() []Peer {
    rows, err := database.DB.Query("SELECT onion_address, nickname, introduced_by, trust_level, last_seen, public_key, is_blocked FROM peers")
    if err != nil { return []Peer{} }
    defer rows.Close()

    var peers []Peer
    for rows.Next() {
        var p Peer
        rows.Scan(&p.OnionAddress, &p.Nickname, &p.IntroducedBy, &p.TrustLevel, &p.LastSeen, &p.PublicKey, &p.Blocked)
        peers = append(peers, p)
    }
    return peers
}

func (pm *PeerManager) GetPublicKey(onion string) string {
    var key string
    err := database.DB.QueryRow("SELECT public_key FROM peers WHERE onion_address = ?", onion).Scan(&key)
    if err != nil { return "" }
    return key
}

func (pm *PeerManager) IsBlocked(onion string) bool {
    var blocked bool
    err := database.DB.QueryRow("SELECT is_blocked FROM peers WHERE onion_address = ?", onion).Scan(&blocked)
    if err == sql.ErrNoRows { return false }
    return blocked
}

func (pm *PeerManager) ToggleBlock(onion string) bool {
    current := pm.IsBlocked(onion)
    _, err := database.DB.Exec("UPDATE peers SET is_blocked = ? WHERE onion_address = ?", !current, onion)
    if err != nil { fmt.Println("DB Error:", err) }
    return !current
}

func (pm *PeerManager) RemovePeer(onion string) {
    database.DB.Exec("DELETE FROM peers WHERE onion_address = ?", onion)
    fmt.Printf("🗑️ Removed peer: %s\n", onion)
}