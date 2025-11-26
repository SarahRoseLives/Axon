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

func StartBackgroundTasks(torCtrl *tor.Controller, pm *discovery.PeerManager, myPubKey string, getNickname func() string) {
	go func() {
		time.Sleep(30 * time.Second)
		for {
			time.Sleep(60 * time.Second)
			if torCtrl.Onion == nil {
				continue
			}
			peers := pm.GetPeers()
			if len(peers) > 0 {
				target := peers[mrand.Intn(len(peers))].OnionAddress
				go PerformHandshake(torCtrl, pm, target, myPubKey, getNickname())
			}
		}
	}()
}

func PerformHandshake(torCtrl *tor.Controller, pm *discovery.PeerManager, target string, myPubKey, myNickname string) {
	if torCtrl.Onion == nil { return }
	client, err := torCtrl.GetHttpClient()
	if err != nil { return }

	payload := HandshakeRequest{
		OnionAddress: torCtrl.Onion.ID + ".onion",
		PublicKey:    myPubKey,
		Nickname:     myNickname,
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

					// 1. Update the Target Peer (Direct Trust)
					// IntroducedBy is empty because we contacted them directly
					if response.PublicKey != "" {
						pm.AddPeer(target, "direct", response.PublicKey, response.Nickname, "")
					}

					// 2. Process Gossip (Transitive Trust)
					// IntroducedBy is 'target' because they gave us this list
					for _, p := range response.Peers {
						clean := Sanitize(p)
						if clean != payload.OnionAddress && !pm.HasPeer(clean) {
							// <--- HERE IS THE MAGIC: We pass 'target' as introducedBy
							pm.AddPeer(clean, "transitive", "", "", target)
						}
					}
				}
				fmt.Printf("✅ Handshake complete with %s (%s)\n", target, payload.Nickname)
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