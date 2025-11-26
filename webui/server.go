package webui

import (
	"axon/discovery"
	"axon/tor"
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

type UIContext struct {
	AppVersion string
	OnionAddr  string
	Status     string
	Peers      []discovery.Peer
}

// Response structure for gossip
type PeerListResponse struct {
	Peers []string `json:"peers"`
}

func Start(port int, torCtrl *tor.Controller, pm *discovery.PeerManager) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("🖥️  AXON UI Ready at http://%s\n", addr)

	// --- BACKGROUND GOSSIP ROUTINE ---
	// Every 60 seconds, pick a random peer and sync lists
	go startGossiping(torCtrl, pm)

	// 1. Render UI
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		status := "Initializing..."
		onion := "Loading..."
		if torCtrl.Ready && torCtrl.Onion != nil {
			status = "Online"
			onion = torCtrl.Onion.ID + ".onion"
		}

		data := UIContext{
			AppVersion: "0.9.4-alpha",
			OnionAddr:  onion,
			Status:     status,
			Peers:      pm.GetPeers(),
		}

		tmpl, err := template.ParseGlob("webui/templates/*.html")
		if err != nil {
			http.Error(w, "Template Error: "+err.Error(), 500)
			return
		}
		err = tmpl.ExecuteTemplate(w, "index.html", data)
		if err != nil {
			log.Printf("Template render error: %v", err)
		}
	})

	// 2. API: Get Peers + Self Identity
	http.HandleFunc("/api/peers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		myAddr := ""
		if torCtrl.Onion != nil {
			myAddr = torCtrl.Onion.ID + ".onion"
		}

		response := map[string]interface{}{
			"self":  myAddr,
			"peers": pm.GetPeers(),
		}

		json.NewEncoder(w).Encode(response)
	})

	// 3. API: Add Peer (Triggers Sync)
	http.HandleFunc("/api/peers/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", 405)
			return
		}

		var req struct {
			OnionAddress string `json:"onion_address"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", 400)
			return
		}

		rawAddr := sanitize(req.OnionAddress)
		if rawAddr == "" {
			http.Error(w, "Empty address", 400)
			return
		}

		if torCtrl.Onion != nil && rawAddr == torCtrl.Onion.ID + ".onion" {
			http.Error(w, "Cannot add yourself", 400)
			return
		}

		if pm.HasPeer(rawAddr) {
			http.Error(w, "Peer already exists", 409)
			return
		}

		// Add as Direct Trust
		pm.AddPeer(rawAddr, "direct")

		// Trigger Sync immediately
		go syncWithPeer(torCtrl, pm, rawAddr)

		w.Write([]byte(`{"status":"success"}`))
	})

	// 4. API: Incoming Handshake (The Receiver)
	http.HandleFunc("/api/peers/announce", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", 405)
			return
		}

		var req struct {
			OnionAddress string `json:"onion_address"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", 400)
			return
		}

		cleanAddr := sanitize(req.OnionAddress)
		if cleanAddr == "" {
			http.Error(w, "Missing address", 400)
			return
		}

		fmt.Printf("👋 Incoming Handshake from: %s\n", cleanAddr)

		// 1. Add them to our list
		pm.AddPeer(cleanAddr, "neighbor")

		// 2. Prepare OUR list to send back (Gossip)
		knownPeers := pm.GetPeers()
		var peerList []string
		for _, p := range knownPeers {
			peerList = append(peerList, p.OnionAddress)
		}

		// 3. Respond with our list
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PeerListResponse{Peers: peerList})
	})

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("❌ Web UI failed to start: %v", err)
	}
}

// --- HELPER FUNCTIONS ---

func sanitize(input string) string {
	s := strings.TrimSpace(input)
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimSuffix(s, "/")
	return s
}

// Background Gossip Loop
func startGossiping(torCtrl *tor.Controller, pm *discovery.PeerManager) {
	// Wait for Tor to warm up
	time.Sleep(30 * time.Second)

	for {
		time.Sleep(60 * time.Second)

		if torCtrl.Onion == nil { continue }

		peers := pm.GetPeers()
		if len(peers) == 0 { continue }

		// Pick a random peer
		randomIndex := rand.Intn(len(peers))
		target := peers[randomIndex]

		// Don't sync with unknown/offline if we can avoid it (optimization for later)
		fmt.Printf("🗣️  Gossiping with %s...\n", target.OnionAddress)
		go syncWithPeer(torCtrl, pm, target.OnionAddress)
	}
}

// syncWithPeer performs the handshake and processes the returned list
func syncWithPeer(torCtrl *tor.Controller, pm *discovery.PeerManager, target string) {
	if torCtrl.Onion == nil { return }

	client, err := torCtrl.GetHttpClient()
	if err != nil {
		fmt.Printf("❌ Tor Client Error: %v\n", err)
		return
	}

	me := torCtrl.Onion.ID + ".onion"
	payload := map[string]string{"onion_address": me}
	jsonPayload, _ := json.Marshal(payload)

	maxRetries := 3
	for i := 1; i <= maxRetries; i++ {
		resp, err := client.Post(
			fmt.Sprintf("http://%s/api/peers/announce", target),
			"application/json",
			bytes.NewBuffer(jsonPayload),
		)

		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				// Parse the gossip response
				var gossip PeerListResponse
				if err := json.NewDecoder(resp.Body).Decode(&gossip); err == nil {
					// Add new peers learned from this neighbor
					for _, newPeer := range gossip.Peers {
						clean := sanitize(newPeer)
						if clean != me && !pm.HasPeer(clean) {
							fmt.Printf("💡 Learned about new peer %s via %s\n", clean, target)
							pm.AddPeer(clean, "transitive") // Mark as transitive
						}
					}
				}
				return
			}
		}
		if i < maxRetries { time.Sleep(10 * time.Second) }
	}
	fmt.Printf("❌ Sync failed with %s\n", target)
}