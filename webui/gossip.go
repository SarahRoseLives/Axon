package webui

import (
    "axon/discovery"
    "axon/files"
    "axon/identity"
    "axon/tor"
    "bytes"
    "crypto/ed25519"
    "encoding/hex"
    "encoding/json"
    "fmt"
    mrand "math/rand"
    "regexp" // <--- ADDED
    "strings"
    "time"
)

type HandshakeRequest struct {
    OnionAddress string `json:"onion_address"`
    PublicKey    string `json:"public_key"` // Chat Key (X25519)
    IdentityKey  string `json:"identity_key"` // Identity Key (Ed25519)
    Nickname     string `json:"nickname"`
    Signature    string `json:"signature"`
}

type PeerListResponse struct {
    Peers     []string `json:"peers"`
    PublicKey string   `json:"public_key"`
    Nickname  string   `json:"nickname"`
}

func StartBackgroundTasks(torCtrl *tor.Controller, pm *discovery.PeerManager, myPubKey string, fileMgr *files.Manager, getNickname func() string, privKey ed25519.PrivateKey) {
    go func() {
        time.Sleep(30 * time.Second)
        for {
            time.Sleep(30 * time.Second)
            if torCtrl.Onion == nil { continue }

            peers := pm.GetPeers()
            if len(peers) > 0 {
                target := peers[mrand.Intn(len(peers))].OnionAddress
                go PerformHandshake(torCtrl, pm, target, myPubKey, fileMgr, getNickname(), privKey)
            }
        }
    }()
}

func PerformHandshake(torCtrl *tor.Controller, pm *discovery.PeerManager, target string, myPubKey string, fileMgr *files.Manager, myNickname string, privKey ed25519.PrivateKey) {
    // 1. Validation Check
    target = Sanitize(target)
    if target == "" { return } // Abort if invalid address

    if torCtrl.Onion == nil { return }

    // Sign the nickname
    sig := identity.Sign(privKey, []byte(myNickname))

    // Derive Public Identity Key
    pubIdKey := hex.EncodeToString(privKey.Public().(ed25519.PublicKey))

    client, err := torCtrl.GetHttpClient()
    if err != nil { return }

    payload := HandshakeRequest{
        OnionAddress: torCtrl.Onion.ID + ".onion",
        PublicKey:    myPubKey,
        IdentityKey:  pubIdKey,
        Nickname:     myNickname,
        Signature:    sig,
    }
    jsonBytes, _ := json.Marshal(payload)

    for i := 1; i <= 2; i++ {
        // Strict HTTP usage with validated .onion address
        resp, err := client.Post(
            fmt.Sprintf("http://%s/api/peers/announce", target),
            "application/json",
            bytes.NewBuffer(jsonBytes),
        )
        if err == nil {
            defer resp.Body.Close()
            if resp.StatusCode == 200 {
                var response PeerListResponse
                if err := json.NewDecoder(resp.Body).Decode(&response); err == nil {
                    if response.PublicKey != "" {
                        pm.AddPeer(target, "direct", response.PublicKey, response.Nickname, "")
                    }

                    for _, p := range response.Peers {
                        // RECURSIVE VALIDATION:
                        // We strictly sanitize peers we learn about from others.
                        clean := Sanitize(p)
                        if clean != "" && clean != payload.OnionAddress && !pm.HasPeer(clean) {
                            pm.AddPeer(clean, "transitive", "", "", target)
                        }
                    }

                    if fileMgr != nil {
                        fmt.Printf("✅ Handshake success with %s. Syncing Library...\n", target)
                        BroadcastManifest(torCtrl, target, fileMgr.GetLocalManifest(), "")
                    } else {
                        fmt.Printf("✅ Handshake connection restored with %s\n", target)
                    }
                }
                return
            }
        }
        time.Sleep(3 * time.Second)
    }
}

// --- SECURITY CRITICAL: STRICT SANITIZATION ---

// Sanitize enforces Tor v3 Address standards.
// It returns an EMPTY string if the address is invalid or not an onion address.
// This prevents SSRF (Server Side Request Forgery) attacks.
func Sanitize(s string) string {
    s = strings.TrimSpace(s)
    s = strings.TrimPrefix(s, "http://")
    s = strings.TrimSuffix(s, "/")

    // Regex Explanation:
    // ^        : Start of string
    // [a-z2-7] : Base32 characters (lowercase letters a-z and numbers 2-7)
    // {56}     : Exactly 56 characters long (Tor v3 standard)
    // \.onion  : Must end in .onion
    // $        : End of string
    match, _ := regexp.MatchString(`^[a-z2-7]{56}\.onion$`, s)

    if !match {
        // If it doesn't match the strict onion pattern, we reject it entirely.
        // This blocks "localhost", IPs "192.168.x.x", or malformed addresses.
        return ""
    }

    return s
}