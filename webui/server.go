package webui

import (
	"axon/chat"
	"axon/discovery"
	"axon/identity"
	"axon/tor"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"
)

type UIContext struct {
	AppVersion    string
	OnionAddr     string
	MyOnionAddr   string
	Status        string
	Peers         []discovery.Peer
	Nickname      string
}

// UPDATED: Now accepts privKey to start Tor internally with the correct restricted handler
func Start(port int, torCtrl *tor.Controller, pm *discovery.PeerManager, identityKey ed25519.PrivateKey) {
	dataDir := fmt.Sprintf("data_%d", port)

	chatMgr, err := chat.NewManager(dataDir)
	if err != nil {
		log.Fatalf("Failed to init chat manager: %v", err)
	}

	profileMgr := identity.NewProfileManager(dataDir)
	getNick := func() string { return profileMgr.GetNickname() }

	// Start background workers
	StartBackgroundTasks(torCtrl, pm, chatMgr.GetMyPublicKey(), getNick)
	StartOutboxLoop(torCtrl, pm, chatMgr, getNick)

	// =========================================================================
	// 🔒 SECURITY ARCHITECTURE: SPLIT MUX
	// =========================================================================

	// 1. PUBLIC MUX (Served over Tor)
	// This router ONLY exposes endpoints required for peer-to-peer communication.
	// It does NOT expose the UI or Admin APIs.
	publicMux := http.NewServeMux()

	// -- Public: Handshake --
	publicMux.HandleFunc("/api/peers/announce", func(w http.ResponseWriter, r *http.Request) {
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

		if pm.IsBlocked(from) {
			http.Error(w, "Blocked", 403)
			return
		}

		fmt.Printf("👋 Handshake from %s (%s)\n", from, req.Nickname)
		pm.AddPeer(from, "neighbor", req.PublicKey, req.Nickname, "")

		knownPeers := pm.GetPeers()
		var peerList []string
		for _, p := range knownPeers {
			peerList = append(peerList, p.OnionAddress)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PeerListResponse{
			Peers:     peerList,
			PublicKey: chatMgr.GetMyPublicKey(),
			Nickname:  profileMgr.GetNickname(),
		})
	})

	// -- Public: Chat Receiver --
	publicMux.HandleFunc("/api/chat/recv", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		var wireMsg chat.WireMessage
		if err := json.NewDecoder(r.Body).Decode(&wireMsg); err != nil {
			return
		}
		from := Sanitize(wireMsg.From)
		if pm.IsBlocked(from) {
			return
		}

		peerKeyHex := pm.GetPublicKey(from)
		if peerKeyHex == "" {
			go PerformHandshake(torCtrl, pm, from, chatMgr.GetMyPublicKey(), profileMgr.GetNickname())
			return
		}

		plaintext, err := chatMgr.Decrypt(peerKeyHex, wireMsg.Ciphertext, wireMsg.Nonce)
		if err != nil {
			return
		}

		msgID := fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Unix())
		msg := chat.Message{
			ID:        msgID,
			From:      from,
			To:        "me",
			Content:   plaintext,
			Timestamp: time.Now(),
			Incoming:  true,
			Status:    "received",
		}
		chatMgr.SaveMessage(from, msg)
		w.Write([]byte(`{"status":"received"}`))
	})

	// -- Public: Feed Receiver --
	publicMux.HandleFunc("/api/feed/recv", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		var wireMsg chat.WireFeedMessage
		if err := json.NewDecoder(r.Body).Decode(&wireMsg); err != nil {
			return
		}
		from := Sanitize(wireMsg.From)
		if pm.IsBlocked(from) {
			return
		}

		msgID := fmt.Sprintf("%d-%s", time.Now().UnixNano(), wireMsg.Nonce)
		msg := chat.Message{
			ID:        msgID,
			From:      from,
			To:        "MESH",
			Content:   wireMsg.Content,
			Timestamp: time.Now(),
		}
		chatMgr.SaveFeedMessage(msg)
		w.Write([]byte(`{"status":"received"}`))
	})

	// -------------------------------------------------------------------------
	// 🚀 START TOR WITH PUBLIC MUX
	// -------------------------------------------------------------------------
	go func() {
		onionAddr, err := torCtrl.Start(dataDir, identityKey, publicMux)
		if err != nil {
			log.Printf("❌ Tor Setup Failed: %v", err)
			return
		}
		fmt.Printf("\n🧅 ONION SERVICE LIVE: %s.onion (Secured API Only)\n", onionAddr)
	}()

	// =========================================================================
	// 2. PRIVATE MUX (Served over Localhost)
	// This router allows FULL ACCESS (UI, Admin, Sending, Deleting).
	// =========================================================================
	privateMux := http.NewServeMux()

	// -- Internal: UI Routes --
	privateMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		status := "Initializing..."
		onion := "Loading..."
		myOnion := ""
		if torCtrl.Ready && torCtrl.Onion != nil {
			status = "Online"
			onion = torCtrl.Onion.ID + ".onion"
			myOnion = torCtrl.Onion.ID + ".onion"
		}
		data := UIContext{
			AppVersion:  "0.9.15 (Secured)",
			OnionAddr:   onion,
			MyOnionAddr: myOnion,
			Status:      status,
			Peers:       pm.GetPeers(),
			Nickname:    profileMgr.GetNickname(),
		}

		tmpl, _ := template.ParseGlob("webui/templates/*.html")
		tmpl.ExecuteTemplate(w, "index.html", data)
	})

	// -- Internal: Management APIs --
	privateMux.HandleFunc("/api/peers/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		var req struct {
			OnionAddress string `json:"onion_address"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.OnionAddress != "" {
			pm.RemovePeer(req.OnionAddress)
			chatMgr.DeleteConversation(req.OnionAddress)
		}
		w.Write([]byte(`{"status":"success"}`))
	})

	privateMux.HandleFunc("/api/peers/block", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		var req struct {
			OnionAddress string `json:"onion_address"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		blocked := false
		if req.OnionAddress != "" {
			blocked = pm.ToggleBlock(req.OnionAddress)
		}
		json.NewEncoder(w).Encode(map[string]bool{"blocked": blocked})
	})

	privateMux.HandleFunc("/api/identity", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var req struct {
				Nickname string `json:"nickname"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if req.Nickname != "" {
				profileMgr.SetNickname(req.Nickname)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"nickname": profileMgr.GetNickname()})
	})

	privateMux.HandleFunc("/api/peers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		myAddr := ""
		if torCtrl.Onion != nil {
			myAddr = torCtrl.Onion.ID + ".onion"
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"self":     myAddr,
			"nickname": profileMgr.GetNickname(),
			"peers":    pm.GetPeers(),
		})
	})

	privateMux.HandleFunc("/api/peers/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		var req struct{ OnionAddress string `json:"onion_address"` }
		json.NewDecoder(r.Body).Decode(&req)
		target := Sanitize(req.OnionAddress)
		if target == "" {
			return
		}
		if torCtrl.Onion != nil && target == torCtrl.Onion.ID+".onion" {
			return
		}

		if !pm.HasPeer(target) {
			pm.AddPeer(target, "direct", "", "", "")
		}
		go PerformHandshake(torCtrl, pm, target, chatMgr.GetMyPublicKey(), profileMgr.GetNickname())
		w.Write([]byte(`{"status":"success"}`))
	})

	// -- Internal: Chat APIs --
	privateMux.HandleFunc("/api/chat/history", func(w http.ResponseWriter, r *http.Request) {
		peer := r.URL.Query().Get("peer")
		msgs := chatMgr.GetHistory(peer)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(msgs)
	})

	privateMux.HandleFunc("/api/chat/status", func(w http.ResponseWriter, r *http.Request) {
		list := chatMgr.GetStatusList()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(list)
	})

	privateMux.HandleFunc("/api/chat/send", func(w http.ResponseWriter, r *http.Request) {
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

		msgID := fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Unix())
		msg := chat.Message{
			ID:        msgID,
			From:      "me",
			To:        req.To,
			Content:   req.Content,
			Timestamp: time.Now(),
			Incoming:  false,
			Status:    "pending",
		}
		chatMgr.SaveMessage(req.To, msg)
		go func() {
			AttemptSendMessage(torCtrl, pm, chatMgr, req.To, msg, profileMgr.GetNickname())
		}()
		w.Write([]byte(`{"status":"queued"}`))
	})

	// -- Internal: Feed APIs --
	privateMux.HandleFunc("/api/feed/history", func(w http.ResponseWriter, r *http.Request) {
		msgs := chatMgr.GetFeedHistory()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(msgs)
	})

	privateMux.HandleFunc("/api/feed/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		var req struct {
			Content string `json:"content"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Content == "" {
			return
		}

		go AttemptSendFeedMessage(torCtrl, pm, chatMgr, req.Content, profileMgr.GetNickname())
		w.Write([]byte(`{"status":"broadcasted"}`))
	})

	// -------------------------------------------------------------------------
	// 🖥️ START LOCAL SERVER WITH PRIVATE MUX
	// -------------------------------------------------------------------------
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("🖥️  AXON UI Ready at http://%s\n", addr)
	fmt.Println("🔒 Admin UI is RESTRICTED to Localhost only.")

	if err := http.ListenAndServe(addr, privateMux); err != nil {
		log.Fatalf("❌ Web UI failed to start: %v", err)
	}
}