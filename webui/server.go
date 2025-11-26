package webui

import (
	"axon/chat"
	"axon/discovery"
	"axon/tor"
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"
)

type UIContext struct {
	AppVersion string
	OnionAddr  string
	Status     string
	Peers      []discovery.Peer
}

func Start(port int, torCtrl *tor.Controller, pm *discovery.PeerManager) {
	// 1. Initialize Chat Manager (Store + Crypto)
	dataDir := fmt.Sprintf("data_%d", port)
	chatMgr, err := chat.NewManager(dataDir)
	if err != nil {
		log.Fatalf("Failed to init chat manager: %v", err)
	}

	// 2. Start Gossip
	StartBackgroundTasks(torCtrl, pm, chatMgr.GetMyPublicKey())

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("🖥️  AXON UI Ready at http://%s\n", addr)

	// --- ROUTES ---

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		status := "Initializing..."
		onion := "Loading..."
		if torCtrl.Ready && torCtrl.Onion != nil {
			status = "Online"
			onion = torCtrl.Onion.ID + ".onion"
		}
		data := UIContext{
			AppVersion: "0.9.8 (Refactored)",
			OnionAddr:  onion,
			Status:     status,
			Peers:      pm.GetPeers(),
		}
		tmpl, _ := template.ParseGlob("webui/templates/*.html")
		tmpl.ExecuteTemplate(w, "index.html", data)
	})

	http.HandleFunc("/api/peers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		myAddr := ""
		if torCtrl.Onion != nil {
			myAddr = torCtrl.Onion.ID + ".onion"
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"self":  myAddr,
			"peers": pm.GetPeers(),
		})
	})

	http.HandleFunc("/api/peers/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		var req struct{ OnionAddress string `json:"onion_address"` }
		json.NewDecoder(r.Body).Decode(&req)

		target := Sanitize(req.OnionAddress)
		if target == "" {
			return
		}
		if torCtrl.Onion != nil && target == torCtrl.Onion.ID + ".onion" {
			return
		}

		if !pm.HasPeer(target) {
			pm.AddPeer(target, "direct", "")
		}

		go PerformHandshake(torCtrl, pm, target, chatMgr.GetMyPublicKey())
		w.Write([]byte(`{"status":"success"}`))
	})

	http.HandleFunc("/api/peers/announce", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		var req HandshakeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return
		}

		from := Sanitize(req.OnionAddress)
		if from == "" {
			return
		}

		fmt.Printf("👋 Handshake from %s (Has Key: %v)\n", from, req.PublicKey != "")
		pm.AddPeer(from, "neighbor", req.PublicKey)

		knownPeers := pm.GetPeers()
		var peerList []string
		for _, p := range knownPeers {
			peerList = append(peerList, p.OnionAddress)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PeerListResponse{
			Peers:     peerList,
			PublicKey: chatMgr.GetMyPublicKey(),
		})
	})

	http.HandleFunc("/api/chat/history", func(w http.ResponseWriter, r *http.Request) {
		peer := r.URL.Query().Get("peer")
		msgs := chatMgr.GetHistory(peer)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(msgs)
	})

	http.HandleFunc("/api/chat/status", func(w http.ResponseWriter, r *http.Request) {
		list := chatMgr.GetStatusList()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(list)
	})

	http.HandleFunc("/api/chat/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		var req struct {
			To      string `json:"to"`
			Content string `json:"content"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		if req.To == "" || req.Content == "" {
			return
		}

		peerKeyHex := pm.GetPublicKey(req.To)
		if peerKeyHex == "" {
			go PerformHandshake(torCtrl, pm, req.To, chatMgr.GetMyPublicKey())
			http.Error(w, "Peer encryption key missing. Handshaking...", 500)
			return
		}

		ciphertext, nonce, err := chatMgr.Encrypt(peerKeyHex, req.Content)
		if err != nil {
			http.Error(w, "Encryption failed", 500)
			return
		}

		// Save Locally
		msg := chat.Message{From: "me", To: req.To, Content: req.Content, Timestamp: time.Now(), Incoming: false}
		chatMgr.SaveMessage(req.To, msg)

		// Send over Tor
		go func() {
			if torCtrl.Onion == nil {
				return
			}
			client, err := torCtrl.GetHttpClient()
			if err != nil {
				return
			}

			wireMsg := chat.WireMessage{
				From:       torCtrl.Onion.ID + ".onion",
				Ciphertext: ciphertext,
				Nonce:      nonce,
			}
			jsonBytes, _ := json.Marshal(wireMsg)

			for i := 0; i < 3; i++ {
				resp, err := client.Post(
					fmt.Sprintf("http://%s/api/chat/recv", req.To),
					"application/json",
					bytes.NewBuffer(jsonBytes),
				)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode == 200 {
						return
					}
				}
				time.Sleep(5 * time.Second)
			}
			fmt.Printf("❌ Failed to send to %s\n", req.To)
		}()

		w.Write([]byte(`{"status":"sent"}`))
	})

	http.HandleFunc("/api/chat/recv", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		var wireMsg chat.WireMessage
		if err := json.NewDecoder(r.Body).Decode(&wireMsg); err != nil {
			return
		}

		from := Sanitize(wireMsg.From)
		peerKeyHex := pm.GetPublicKey(from)
		if peerKeyHex == "" {
			fmt.Printf("⚠️ Received msg from %s but missing key. Dropping & Handshaking.\n", from)
			go PerformHandshake(torCtrl, pm, from, chatMgr.GetMyPublicKey())
			return
		}

		plaintext, err := chatMgr.Decrypt(peerKeyHex, wireMsg.Ciphertext, wireMsg.Nonce)
		if err != nil {
			fmt.Printf("⚠️ Decryption failed from %s: %v\n", from, err)
			return
		}

		msg := chat.Message{From: from, To: "me", Content: plaintext, Timestamp: time.Now(), Incoming: true}
		chatMgr.SaveMessage(from, msg)

		fmt.Printf("📩 Encrypted msg received from %s\n", from)
		w.Write([]byte(`{"status":"received"}`))
	})

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("❌ Web UI failed to start: %v", err)
	}
}