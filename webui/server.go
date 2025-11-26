package webui

import (
	"axon/discovery"
	"axon/tor"
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
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

func Start(port int, torCtrl *tor.Controller, pm *discovery.PeerManager) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("🖥️  AXON UI Ready at http://%s\n", addr)

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

	// 2. API: Add Peer (Outgoing Handshake)
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

		// --- SANITIZATION ---
		rawAddr := strings.TrimSpace(req.OnionAddress)
		rawAddr = strings.TrimPrefix(rawAddr, "http://")
		rawAddr = strings.TrimSuffix(rawAddr, "/")

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

		// A. Add locally
		pm.AddPeer(rawAddr, "direct")

		// B. Announce (Handshake)
		go func(target, me string) {
			if torCtrl.Onion == nil { return }

			fmt.Printf("📡 Handshaking with %s...\n", target)
			client, err := torCtrl.GetHttpClient()
			if err != nil {
				fmt.Printf("❌ Tor Client Error: %v\n", err)
				return
			}

			payload := map[string]string{"onion_address": me + ".onion"}
			jsonPayload, _ := json.Marshal(payload)

			maxRetries := 3
			for i := 1; i <= maxRetries; i++ {
				fmt.Printf("   ... Attempt %d/%d\n", i, maxRetries)

				resp, err := client.Post(
					fmt.Sprintf("http://%s/api/peers/announce", target),
					"application/json",
					bytes.NewBuffer(jsonPayload),
				)

				if err == nil {
					resp.Body.Close()
					if resp.StatusCode == 200 || resp.StatusCode == 202 {
						fmt.Printf("✅ Handshake success! %s knows us now.\n", target)
						return
					}
					fmt.Printf("⚠️ Peer returned status: %d\n", resp.StatusCode)
				} else {
					fmt.Printf("⚠️ Connection failed: %v\n", err)
				}

				if i < maxRetries {
					time.Sleep(10 * time.Second)
				}
			}

			fmt.Printf("❌ Handshake gave up after %d attempts.\n", maxRetries)

		}(rawAddr, torCtrl.Onion.ID)

		w.Write([]byte(`{"status":"success"}`))
	})

	// 3. API: Incoming Handshake
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

		cleanAddr := strings.TrimSpace(req.OnionAddress)
		if cleanAddr == "" {
			http.Error(w, "Missing address", 400)
			return
		}

		fmt.Printf("👋 Received Handshake from: %s\n", cleanAddr)
		pm.AddPeer(cleanAddr, "neighbor")
		w.Write([]byte(`{"status":"acknowledged"}`))
	})

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("❌ Web UI failed to start: %v", err)
	}
}