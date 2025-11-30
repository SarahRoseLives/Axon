package webui

import (
    "axon/chat"
    "axon/discovery"
    "axon/files"
    "axon/identity"
    "axon/tor"
    "bytes"
    "crypto/ed25519"
    "encoding/json"
    "fmt"
    "html/template"
    "log"
    "net/http"
    "strconv"
    "time"
)

type UIContext struct {
    AppVersion   string
    OnionAddr    string
    MyOnionAddr  string
    Status       string
    Peers        []discovery.Peer
    Nickname     string
    SharedCount  int
}

func Start(port int, torCtrl *tor.Controller, pm *discovery.PeerManager, identityKey ed25519.PrivateKey) {
    dataDir := fmt.Sprintf("data_%d", port)

    chatMgr, err := chat.NewManager(dataDir)
    if err != nil {
        log.Fatalf("Failed to init chat manager: %v", err)
    }

    profileMgr := identity.NewProfileManager(dataDir)
    fileMgr := files.NewManager(dataDir)
    getNick := func() string { return profileMgr.GetNickname() }

    StartBackgroundTasks(torCtrl, pm, chatMgr.GetMyPublicKey(), fileMgr, getNick, identityKey)
    StartOutboxLoop(torCtrl, pm, chatMgr, fileMgr, getNick, identityKey)

    renderTemplate := func(w http.ResponseWriter, tmplName string, data interface{}) {
        tmpl, err := template.ParseGlob("webui/templates/*.html")
        if err != nil {
            fmt.Printf("🔥 TEMPLATE PARSE ERROR: %v\n", err)
            http.Error(w, fmt.Sprintf("500 Template Error: %v", err), 500)
            return
        }
        buf := new(bytes.Buffer)
        err = tmpl.ExecuteTemplate(buf, tmplName, data)
        if err != nil {
            fmt.Printf("🔥 TEMPLATE EXEC ERROR: %v\n", err)
            http.Error(w, fmt.Sprintf("500 Render Error: %v", err), 500)
            return
        }
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        w.Write(buf.Bytes())
    }

    publicMux := http.NewServeMux()
    setupPublicRoutes(publicMux, pm, chatMgr, fileMgr, torCtrl, profileMgr, identityKey)

    loggingMiddleware := func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            next.ServeHTTP(w, r)
        })
    }

    go func() {
        onionAddr, err := torCtrl.Start(dataDir, identityKey, loggingMiddleware(publicMux))
        if err != nil {
            log.Printf("❌ Tor Setup Failed: %v", err)
            return
        }
        fmt.Printf("\n🧅 ONION SERVICE LIVE: %s.onion\n", onionAddr)
    }()

    privateMux := http.NewServeMux()
    privateMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        status := "Initializing..."
        onion := "Loading..."
        if torCtrl.Ready && torCtrl.Onion != nil {
            status = "Online"
            onion = torCtrl.Onion.ID + ".onion"
        }
        data := UIContext{
            AppVersion:  "0.9.37 (Secure)",
            OnionAddr:   onion,
            Status:      status,
            Peers:       pm.GetPeers(),
            Nickname:    profileMgr.GetNickname(),
            SharedCount: len(fileMgr.LocalFiles),
        }
        renderTemplate(w, "index.html", data)
    })

    setupPrivateAPIs(privateMux, pm, chatMgr, fileMgr, torCtrl, profileMgr, identityKey)

    addr := fmt.Sprintf("127.0.0.1:%d", port)
    fmt.Printf("🖥️  AXON UI Ready at http://%s\n", addr)
    http.ListenAndServe(addr, privateMux)
}

func setupPublicRoutes(mux *http.ServeMux, pm *discovery.PeerManager, chatMgr *chat.Manager, fileMgr *files.Manager, torCtrl *tor.Controller, profileMgr *identity.ProfileManager, identityKey ed25519.PrivateKey) {
    mux.HandleFunc("/api/peers/announce", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost { return }
        var req HandshakeRequest
        json.NewDecoder(r.Body).Decode(&req)

        // --- SECURITY: Strict Onion Check ---
        from := Sanitize(req.OnionAddress)
        // If Sanitize returns empty, it's a blocked/invalid address.
        if from == "" || pm.IsBlocked(from) {
            fmt.Println("⚠️ Rejected Handshake: Invalid Address or Blocked")
            return
        }

        // Verify Identity Signature
        validSig := false
        if req.IdentityKey != "" && req.Signature != "" {
            if identity.Verify(req.IdentityKey, []byte(req.Nickname), req.Signature) {
                validSig = true
            } else {
                 fmt.Printf("⚠️ Signature Invalid for %s\n", from)
            }
        }

        finalNick := "Anonymous"
        if validSig { finalNick = req.Nickname }

        fmt.Printf("👋 Handshake received from %s (%s)\n", from, finalNick)
        pm.AddPeer(from, "neighbor", req.PublicKey, finalNick, "")

        go func() {
            time.Sleep(2 * time.Second)
            BroadcastManifest(torCtrl, from, fileMgr.GetLocalManifest(), "")
        }()

        peers := pm.GetPeers()
        var list []string
        for _, p := range peers { list = append(list, p.OnionAddress) }
        json.NewEncoder(w).Encode(PeerListResponse{Peers: list, PublicKey: chatMgr.GetMyPublicKey(), Nickname: profileMgr.GetNickname()})
    })

    mux.HandleFunc("/api/chat/recv", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost { return }
        var wire chat.WireMessage
        json.NewDecoder(r.Body).Decode(&wire)

        // --- SECURITY: Strict Onion Check ---
        from := Sanitize(wire.From)
        if from == "" { return }

        key := pm.GetPublicKey(from)
        if key == "" { return }
        txt, _ := chatMgr.Decrypt(key, wire.Ciphertext, wire.Nonce)

        msgID := wire.ID
        if msgID == "" { msgID = fmt.Sprintf("%d", time.Now().UnixNano()) }

        chatMgr.SaveMessage(from, chat.Message{
            ID: msgID,
            From: from, To: "me", Content: txt, Timestamp: time.Now(), Incoming: true, Status: "received",
        })
        w.Write([]byte("OK"))
    })

    mux.HandleFunc("/api/file/manifest", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost { return }
        var list []files.FileMetadata
        if err := json.NewDecoder(r.Body).Decode(&list); err != nil { return }
        if len(list) > 0 {
            // --- SECURITY: Strict Onion Check ---
            origin := Sanitize(list[0].Owner)
            if origin == "" { return }

            didLearnNew := fileMgr.ProcessRemoteManifest(origin, list)
            if didLearnNew {
                go ForwardGossip(torCtrl, pm, origin, list)
            }
        }
        w.Write([]byte("OK"))
    })

    mux.HandleFunc("/api/file/chunk", func(w http.ResponseWriter, r *http.Request) {
        id := r.URL.Query().Get("id")
        idx, _ := strconv.Atoi(r.URL.Query().Get("idx"))
        data, err := fileMgr.GetChunk(id, idx)
        if err != nil { http.Error(w, "Not found", 404); return }
        w.Write(data)
    })

    mux.HandleFunc("/api/feed/recv", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost { return }
        var wireMsg chat.WireFeedMessage
        json.NewDecoder(r.Body).Decode(&wireMsg)

        // --- SECURITY: Strict Onion Check ---
        from := Sanitize(wireMsg.From)
        if from == "" { return }

        if pm.IsBlocked(from) { return }
        msgID := fmt.Sprintf("%d-%s", time.Now().UnixNano(), wireMsg.Nonce)
        msg := chat.Message{ID: msgID, From: from, To: "MESH", Content: wireMsg.Content, Timestamp: time.Now()}
        chatMgr.SaveFeedMessage(msg)
        w.Write([]byte("OK"))
    })
}

func setupPrivateAPIs(mux *http.ServeMux, pm *discovery.PeerManager, chatMgr *chat.Manager, fileMgr *files.Manager, torCtrl *tor.Controller, profileMgr *identity.ProfileManager, identityKey ed25519.PrivateKey) {
    mux.HandleFunc("/api/peers", func(w http.ResponseWriter, r *http.Request) {
        addr := ""
        if torCtrl.Onion != nil { addr = torCtrl.Onion.ID + ".onion" }
        json.NewEncoder(w).Encode(map[string]interface{}{"self": addr, "peers": pm.GetPeers()})
    })

    // UI: Add Peer Manually
    mux.HandleFunc("/api/peers/add", func(w http.ResponseWriter, r *http.Request) {
        var req struct{ OnionAddress string `json:"onion_address"` }
        json.NewDecoder(r.Body).Decode(&req)

        // --- SECURITY: Strict Onion Check ---
        target := Sanitize(req.OnionAddress)

        if target != "" {
            pm.AddPeer(target, "direct", "", "", "")
            go PerformHandshake(torCtrl, pm, target, chatMgr.GetMyPublicKey(), fileMgr, profileMgr.GetNickname(), identityKey)
            w.Write([]byte("OK"))
        } else {
            http.Error(w, "Invalid Onion Address", 400)
        }
    })

    mux.HandleFunc("/api/chat/status", func(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(chatMgr.GetStatusList())
    })
    mux.HandleFunc("/api/chat/history", func(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(chatMgr.GetHistory(r.URL.Query().Get("peer")))
    })
    mux.HandleFunc("/api/chat/send", func(w http.ResponseWriter, r *http.Request) {
        var req struct { To, Content string }
        json.NewDecoder(r.Body).Decode(&req)

        to := Sanitize(req.To) // Sanitize output too just in case
        if to == "" { return }

        msg := chat.Message{ID: fmt.Sprintf("%d", time.Now().UnixNano()), From: "me", To: to, Content: req.Content, Timestamp: time.Now(), Status: "pending"}
        chatMgr.SaveMessage(to, msg)
        go AttemptSendMessage(torCtrl, pm, chatMgr, to, msg, profileMgr.GetNickname(), identityKey)
        w.Write([]byte("OK"))
    })
    mux.HandleFunc("/api/files/status", func(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(fileMgr.GetTransfers())
    })
    mux.HandleFunc("/api/files/search", func(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(fileMgr.Search(r.URL.Query().Get("q")))
    })
    mux.HandleFunc("/api/files/download", func(w http.ResponseWriter, r *http.Request) {
        var req struct {
            PeerID   string `json:"peer_id"`
            FileID   string `json:"file_id"`
            FileName string `json:"file_name"`
            Size     int64  `json:"size"`
        }
        json.NewDecoder(r.Body).Decode(&req)

        peerID := Sanitize(req.PeerID)
        if peerID == "" { return }

        fmt.Printf("📥 Download Request: %s from %s\n", req.FileName, peerID)
        go PerformDownload(torCtrl, fileMgr, peerID, req.FileID, req.FileName, req.Size)
        w.Write([]byte("OK"))
    })
    mux.HandleFunc("/api/files/refresh", func(w http.ResponseWriter, r *http.Request) {
        fileMgr.ScanSharedFolder()
        go BroadcastToAll(torCtrl, pm, fileMgr.GetLocalManifest())
        w.Write([]byte("OK"))
    })
    mux.HandleFunc("/api/feed/history", func(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(chatMgr.GetFeedHistory())
    })
    mux.HandleFunc("/api/feed/send", func(w http.ResponseWriter, r *http.Request) {
        var req struct { Content string `json:"content"` }
        json.NewDecoder(r.Body).Decode(&req)
        if req.Content != "" {
            go AttemptSendFeedMessage(torCtrl, pm, chatMgr, req.Content, profileMgr.GetNickname())
        }
        w.Write([]byte("OK"))
    })
    mux.HandleFunc("/api/identity", func(w http.ResponseWriter, r *http.Request) {
        if r.Method == "POST" {
            var req struct{ Nickname string `json:"nickname"` }
            json.NewDecoder(r.Body).Decode(&req)
            profileMgr.SetNickname(req.Nickname)
        }
        w.Write([]byte("OK"))
    })
}