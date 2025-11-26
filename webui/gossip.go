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

// Updated Protocol Structs
type HandshakeRequest struct {
	OnionAddress string `json:"onion_address"`
	PublicKey    string `json:"public_key"`
	Nickname     string `json:"nickname"` // <--- NEW
}

type PeerListResponse struct {
	Peers     []string `json:"peers"`
	PublicKey string   `json:"public_key"`
	Nickname  string   `json:"nickname"` // <--- NEW
}

// StartBackgroundTasks now needs 'getNickname' func to always send fresh name
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
		Nickname:     myNickname, // <--- Send our name
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

					// Save their Key AND Nickname
					if response.PublicKey != "" {
						pm.AddPeer(target, "direct", response.PublicKey, response.Nickname)
					}

					for _, p := range response.Peers {
						clean := Sanitize(p)
						if clean != payload.OnionAddress && !pm.HasPeer(clean) {
							pm.AddPeer(clean, "transitive", "", "")
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