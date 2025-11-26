package webui

import (
	"axon/discovery"
	"axon/tor"
	"bytes"
	"encoding/json"
	"fmt"
	mrand "math/rand"
	"strings"
	"time"
)

// Data structs needed for gossip
type HandshakeRequest struct {
	OnionAddress string `json:"onion_address"`
	PublicKey    string `json:"public_key"`
}

type PeerListResponse struct {
	Peers     []string `json:"peers"`
	PublicKey string   `json:"public_key"`
}

// StartBackgroundTasks initiates the gossip loop
func StartBackgroundTasks(torCtrl *tor.Controller, pm *discovery.PeerManager, myPubKey string) {
	go func() {
		time.Sleep(30 * time.Second) // Warmup
		for {
			time.Sleep(60 * time.Second)
			if torCtrl.Onion == nil {
				continue
			}
			peers := pm.GetPeers()
			if len(peers) > 0 {
				target := peers[mrand.Intn(len(peers))].OnionAddress
				go PerformHandshake(torCtrl, pm, target, myPubKey)
			}
		}
	}()
}

func PerformHandshake(torCtrl *tor.Controller, pm *discovery.PeerManager, target string, myPubKey string) {
	if torCtrl.Onion == nil {
		return
	}
	client, err := torCtrl.GetHttpClient()
	if err != nil {
		return
	}

	payload := HandshakeRequest{
		OnionAddress: torCtrl.Onion.ID + ".onion",
		PublicKey:    myPubKey,
	}
	jsonBytes, _ := json.Marshal(payload)

	for i := 1; i <= 3; i++ {
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

					// Save their key
					if response.PublicKey != "" {
						pm.AddPeer(target, "direct", response.PublicKey)
					}

					// Process Gossip
					for _, p := range response.Peers {
						clean := Sanitize(p)
						if clean != payload.OnionAddress && !pm.HasPeer(clean) {
							pm.AddPeer(clean, "transitive", "")
						}
					}
				}
				fmt.Printf("✅ Handshake complete with %s\n", target)
				return
			}
		}
		time.Sleep(5 * time.Second)
	}
}

func Sanitize(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimSuffix(s, "/")
	return s
}