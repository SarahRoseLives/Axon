package webui

import (
	"axon/chat"
	"axon/discovery"
	"axon/identity"
	"axon/tor"
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
	Nickname   string
}

func Start(port int, torCtrl *tor.Controller, pm *discovery.PeerManager) {
	dataDir := fmt.Sprintf("data_%d", port)

	chatMgr, err := chat.NewManager(dataDir)
	if err != nil {
		log.Fatalf("Failed to init chat manager: %v", err)
	}

	profileMgr := identity.NewProfileManager(dataDir)

	getNick := func() string { return profileMgr.GetNickname() }

	StartBackgroundTasks(torCtrl, pm, chatMgr.GetMyPublicKey(), getNick)
	StartOutboxLoop(torCtrl, pm, chatMgr, getNick)

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
			AppVersion: "0.9.13 (Peer Mgmt)",
			OnionAddr:  onion,
			Status:     status,
			Peers:      pm.GetPeers(),
			Nickname:   profileMgr.GetNickname(),
		}
		tmpl, _ := template.ParseGlob("webui/templates/*.html")
		tmpl.ExecuteTemplate(w, "index.html", data)
	})

	// --- MANAGEMENT APIs ---

	http.HandleFunc("/api/peers/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { return }
		var req struct { OnionAddress string `json:"onion_address"` }
		json.NewDecoder(r.Body).Decode(&req)

		if req.OnionAddress != "" {
			pm.RemovePeer(req.OnionAddress)
			chatMgr.DeleteConversation(req.OnionAddress)
		}
		w.Write([]byte(`{"status":"success"}`))
	})

	http.HandleFunc("/api/peers/block", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { return }
		var req struct { OnionAddress string `json:"onion_address"` }
		json.NewDecoder(r.Body).Decode(&req)

		blocked := false
		if req.OnionAddress != "" {
			blocked = pm.ToggleBlock(req.OnionAddress)
		}
		json.NewEncoder(w).Encode(map[string]bool{"blocked": blocked})
	})

	// --- EXISTING APIs ---

	http.HandleFunc("/api/identity", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var req struct { Nickname string `json:"nickname"` }
			json.NewDecoder(r.Body).Decode(&req)
			if req.Nickname != "" { profileMgr.SetNickname(req.Nickname) }
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"nickname": profileMgr.GetNickname()})
	})

	http.HandleFunc("/api/peers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		myAddr := ""
		if torCtrl.Onion != nil { myAddr = torCtrl.Onion.ID + ".onion" }
		json.NewEncoder(w).Encode(map[string]interface{}{
			"self":     myAddr,
			"nickname": profileMgr.GetNickname(),
			"peers":    pm.GetPeers(),
		})
	})

	http.HandleFunc("/api/peers/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { return }
		var req struct{ OnionAddress string `json:"onion_address"` }
		json.NewDecoder(r.Body).Decode(&req)
		target := Sanitize(req.OnionAddress)
		if target == "" { return }
		if torCtrl.Onion != nil && target == torCtrl.Onion.ID + ".onion" { return }
		if !pm.HasPeer(target) { pm.AddPeer(target, "direct", "", "") }
		go PerformHandshake(torCtrl, pm, target, chatMgr.GetMyPublicKey(), profileMgr.GetNickname())
		w.Write([]byte(`{"status":"success"}`))
	})

	http.HandleFunc("/api/peers/announce", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { return }
		var req HandshakeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil { return }
		from := Sanitize(req.OnionAddress)
		if from == "" { return }

		// BLOCK CHECK
		if pm.IsBlocked(from) {
			fmt.Printf("🛑 Blocked handshake attempt from %s\n", from)
			http.Error(w, "Blocked", 403)
			return
		}

		fmt.Printf("👋 Handshake from %s (%s)\n", from, req.Nickname)
		pm.AddPeer(from, "neighbor", req.PublicKey, req.Nickname)
		knownPeers := pm.GetPeers()
		var peerList []string
		for _, p := range knownPeers { peerList = append(peerList, p.OnionAddress) }
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PeerListResponse{
			Peers:     peerList,
			PublicKey: chatMgr.GetMyPublicKey(),
			Nickname:  profileMgr.GetNickname(),
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
		if r.Method != http.MethodPost { return }
		var req struct { To string `json:"to"`; Content string `json:"content"` }
		json.NewDecoder(r.Body).Decode(&req)
		if req.To == "" || req.Content == "" { return }

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
		go func() { AttemptSendMessage(torCtrl, pm, chatMgr, req.To, msg, profileMgr.GetNickname()) }()
		w.Write([]byte(`{"status":"queued"}`))
	})

	http.HandleFunc("/api/chat/recv", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { return }
		var wireMsg chat.WireMessage
		if err := json.NewDecoder(r.Body).Decode(&wireMsg); err != nil { return }

		from := Sanitize(wireMsg.From)

		// BLOCK CHECK
		if pm.IsBlocked(from) {
			fmt.Printf("🛑 Dropped message from blocked peer %s\n", from)
			return
		}

		peerKeyHex := pm.GetPublicKey(from)
		if peerKeyHex == "" {
			go PerformHandshake(torCtrl, pm, from, chatMgr.GetMyPublicKey(), profileMgr.GetNickname())
			return
		}

		plaintext, err := chatMgr.Decrypt(peerKeyHex, wireMsg.Ciphertext, wireMsg.Nonce)
		if err != nil { return }

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

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("❌ Web UI failed to start: %v", err)
	}
}