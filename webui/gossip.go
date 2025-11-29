package webui

import (
    "axon/discovery"
    "axon/files"
    "axon/tor"
    "bytes"
    "encoding/json"
    "fmt"
    mrand "math/rand"
    "strings"
    "time"
)

type HandshakeRequest struct {
    OnionAddress string `json:"onion_address"`
    PublicKey    string `json:"public_key"`
    Nickname     string `json:"nickname"`
}

type PeerListResponse struct {
    Peers     []string `json:"peers"`
    PublicKey string   `json:"public_key"`
    Nickname  string   `json:"nickname"`
}

func StartBackgroundTasks(torCtrl *tor.Controller, pm *discovery.PeerManager, myPubKey string, fileMgr *files.Manager, getNickname func() string) {
    go func() {
        time.Sleep(30 * time.Second) // Let Tor warm up
        for {
            time.Sleep(30 * time.Second) // Check for new peers/re-connections
            if torCtrl.Onion == nil { continue }

            peers := pm.GetPeers()
            if len(peers) > 0 {
                // Pick a random peer to ensure connectivity
                target := peers[mrand.Intn(len(peers))].OnionAddress
                // We pass fileMgr here so we can gossip if the handshake succeeds
                go PerformHandshake(torCtrl, pm, target, myPubKey, fileMgr, getNickname())
            }
        }
    }()
}

func PerformHandshake(torCtrl *tor.Controller, pm *discovery.PeerManager, target string, myPubKey string, fileMgr *files.Manager, myNickname string) {
    if torCtrl.Onion == nil { return }
    client, err := torCtrl.GetHttpClient()
    if err != nil { return }

    payload := HandshakeRequest{
        OnionAddress: torCtrl.Onion.ID + ".onion",
        PublicKey:    myPubKey,
        Nickname:     myNickname,
    }
    jsonBytes, _ := json.Marshal(payload)

    // Retry loop for robustness
    for i := 1; i <= 2; i++ {
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
                    // 1. Update Trust
                    if response.PublicKey != "" {
                        pm.AddPeer(target, "direct", response.PublicKey, response.Nickname, "")
                    }

                    // 2. Process Transitive Peers
                    for _, p := range response.Peers {
                        clean := Sanitize(p)
                        if clean != payload.OnionAddress && !pm.HasPeer(clean) {
                            pm.AddPeer(clean, "transitive", "", "", target)
                        }
                    }

                    // 3. GOSSIP FILES (Event-Driven)
                    fmt.Printf("✅ Handshake success with %s. Syncing Library...\n", target)

                    // FIX: Added empty string "" as forceOwner argument
                    BroadcastManifest(torCtrl, target, fileMgr.GetLocalManifest(), "")
                }
                return
            }
        }
        time.Sleep(3 * time.Second)
    }
}

func Sanitize(s string) string {
    s = strings.TrimSpace(s)
    s = strings.TrimPrefix(s, "http://")
    s = strings.TrimSuffix(s, "/")
    return s
}