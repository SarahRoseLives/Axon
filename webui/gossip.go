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
	"regexp"
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

// StartBackgroundTasks runs the continuous gossip loop
func StartBackgroundTasks(torCtrl *tor.Controller, pm *discovery.PeerManager, myPubKey string, fileMgr *files.Manager, getNickname func() string, privKey ed25519.PrivateKey) {
	go func() {
		// Wait for Tor to boot and stabilize
		time.Sleep(30 * time.Second)

		for {
			// Gossip interval
			time.Sleep(30 * time.Second)

			if torCtrl.Onion == nil {
				continue
			}

			peers := pm.GetPeers()
			if len(peers) > 0 {
				// Pick a random peer to handshake/gossip with
				target := peers[mrand.Intn(len(peers))].OnionAddress
				go PerformHandshake(torCtrl, pm, target, myPubKey, fileMgr, getNickname(), privKey)
			}
		}
	}()
}

// PerformHandshake initiates a connection (HTTPS then HTTP) to a remote peer
func PerformHandshake(torCtrl *tor.Controller, pm *discovery.PeerManager, target string, myPubKey string, fileMgr *files.Manager, myNickname string, privKey ed25519.PrivateKey) {
	// 1. Validation Check
	target = Sanitize(target)
	if target == "" || torCtrl.Onion == nil {
		return
	}

	// Sign the nickname to prove identity
	sig := identity.Sign(privKey, []byte(myNickname))
	pubIdKey := hex.EncodeToString(privKey.Public().(ed25519.PublicKey))

	// Get the Tor Client (Configured to ignore Self-Signed Cert errors)
	client, err := torCtrl.GetHttpClient()
	if err != nil {
		return
	}

	payload := HandshakeRequest{
		OnionAddress: torCtrl.Onion.ID + ".onion",
		PublicKey:    myPubKey,
		IdentityKey:  pubIdKey,
		Nickname:     myNickname,
		Signature:    sig,
	}
	jsonBytes, _ := json.Marshal(payload)

	// 2. SMART PROTOCOL RETRY
	// Go Peers listen on 443 (HTTPS). Android Peers default to 80 (HTTP).
	// We try HTTPS first. If it fails (Connection Refused), we try HTTP.
	protocols := []string{"https", "http"}

	for _, proto := range protocols {
		url := fmt.Sprintf("%s://%s/api/peers/announce", proto, target)
		fmt.Printf("🤝 Handshake %s -> %s (%s)...\n", payload.OnionAddress[:16], target[:16], proto)

		resp, err := client.Post(
			url,
			"application/json",
			bytes.NewBuffer(jsonBytes),
		)

		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				var response PeerListResponse
				if err := json.NewDecoder(resp.Body).Decode(&response); err == nil {
					// 1. Add/Update Peer
					if response.PublicKey != "" {
						pm.AddPeer(target, "direct", response.PublicKey, response.Nickname, "")
					}

					// 2. Gossip (Transitive Peers)
					for _, p := range response.Peers {
						clean := Sanitize(p)
						if clean != "" && clean != payload.OnionAddress && !pm.HasPeer(clean) {
							pm.AddPeer(clean, "transitive", "", "", target)
						}
					}

					// 3. Sync Files
					if fileMgr != nil {
						fmt.Printf("✅ Handshake success with %s. Syncing Library...\n", target)
						// Ensure Broadcast uses the same successful protocol?
						// For now, BroadcastManifest blindly uses https, you might want to update it to take a protocol arg.
						// But usually, once we know they are online, we assume standard config.
						BroadcastManifest(torCtrl, target, fileMgr.GetLocalManifest(), "")
					}

					// Success! Stop trying other protocols.
					return
				}
			} else {
				fmt.Printf("⚠️ Handshake Rejected by %s: HTTP %d\n", target, resp.StatusCode)
				// If rejected (400/500), the protocol worked but logic failed. Don't retry HTTP.
				return
			}
		} else {
			// Log specific error to help debugging
			// "connection refused" usually means "Try the other port"
			// "TTL expired" means "Peer offline"
			errMsg := err.Error()
			if strings.Contains(errMsg, "connection refused") {
				fmt.Printf("⚠️ %s refused. Falling back...\n", proto)
			} else {
				fmt.Printf("⚠️ Network Error to %s: %v\n", target, err)
			}
		}

		// Small delay before fallback
		time.Sleep(500 * time.Millisecond)
	}
}

// Sanitize enforces Tor v3 Address standards.
func Sanitize(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimSuffix(s, "/")

	if idx := strings.Index(s, ":"); idx != -1 {
		s = s[:idx]
	}

	match, _ := regexp.MatchString(`^[a-z2-7]{56}\.onion$`, s)
	if !match {
		return ""
	}
	return s
}