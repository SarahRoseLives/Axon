package webui

import (
	"axon/chat"
	"axon/discovery"
	"axon/files"
	"axon/identity"
	"axon/tor"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"html/template"
	"io"
	"log"
	"math/big"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// --- HELPER: Generate Self-Signed Cert in Memory ---
func generateSelfSignedCert() (tls.Certificate, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"Axon Node"}},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour * 24 * 365 * 10), // 10 years
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})

	return tls.X509KeyPair(certPEM, keyPEM)
}

// ManifestPayload wraps the Bloom Filter with ownership info
type ManifestPayload struct {
	Owner  string             `json:"owner"`
	Filter *files.BloomFilter `json:"filter"`
}

type UIContext struct {
	AppVersion  string
	OnionAddr   string
	MyOnionAddr string
	Status      string
	Peers       []discovery.Peer
	Nickname    string
	SharedCount int
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
			fmt.Printf("🔹 [%s] %s\n", r.Method, r.URL.Path)
			next.ServeHTTP(w, r)
		})
	}

	// --- TLS / ONION LISTENER ---
	go func() {
		// 1. Generate the Self-Signed Cert
		cert, err := generateSelfSignedCert()
		if err != nil {
			log.Fatalf("❌ Failed to generate cert: %v", err)
		}

		// 2. Start Tor and get the Listener (On port 443)
		onionListener, err := torCtrl.Start(dataDir, identityKey)
		if err != nil {
			log.Printf("❌ Tor Setup Failed: %v", err)
			return
		}
		fmt.Printf("\n🧅 ONION SERVICE LIVE: %s.onion (HTTPS Enabled)\n", onionListener.ID)

		// 3. Wrap the Onion Listener in TLS
		// This enables the Go node to accept the SSL Handshake from Flutter
		tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}}
		tlsListener := tls.NewListener(onionListener, tlsConfig)

		// 4. Serve traffic
		server := &http.Server{
			Handler: loggingMiddleware(publicMux),
		}
		if err := server.Serve(tlsListener); err != nil {
			log.Printf("❌ Onion Server Crashed: %v", err)
		}
	}()

	// --- LOCALHOST UI ---
	privateMux := http.NewServeMux()
	privateMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		status := "Initializing..."
		onion := "Loading..."
		if torCtrl.Ready && torCtrl.Onion != nil {
			status = "Online"
			onion = torCtrl.Onion.ID + ".onion"
		}
		data := UIContext{
			AppVersion:  "0.9.40 (TLS-Fix)",
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

	if err := http.ListenAndServe(addr, privateMux); err != nil {
		log.Fatalf("🔥 Server crashed: %v", err)
	}
}

// ... (Keep setupPublicRoutes and setupPrivateAPIs as they were in previous version) ...
// Ensure you include the implementations of setupPublicRoutes and setupPrivateAPIs here
// or keep them in the file if you are only replacing the Start function.

func setupPublicRoutes(mux *http.ServeMux, pm *discovery.PeerManager, chatMgr *chat.Manager, fileMgr *files.Manager, torCtrl *tor.Controller, profileMgr *identity.ProfileManager, identityKey ed25519.PrivateKey) {
	// ... (Same as before) ...
	// --- HANDSHAKE ---
	mux.HandleFunc("/api/peers/announce", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}

		bodyBytes, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		var req HandshakeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Bad JSON", 400)
			return
		}

		rawOnion := req.OnionAddress
		from := Sanitize(rawOnion)

		if from == "" || pm.IsBlocked(from) {
			return
		}

		pm.AddPeer(from, "neighbor", req.PublicKey, req.Nickname, "")

		peers := pm.GetPeers()
		var list []string
		for _, p := range peers {
			list = append(list, p.OnionAddress)
		}
		json.NewEncoder(w).Encode(PeerListResponse{Peers: list, PublicKey: chatMgr.GetMyPublicKey(), Nickname: profileMgr.GetNickname()})
	})

	mux.HandleFunc("/api/chat/recv", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { return }
		var wire chat.WireMessage
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil { return }

		from := Sanitize(wire.From)
		key := pm.GetPublicKey(from)
		if key == "" { http.Error(w, "Unknown Peer", 403); return }

		txt, err := chatMgr.Decrypt(key, wire.Ciphertext, wire.Nonce)
		if err != nil { http.Error(w, "Decrypt Fail", 500); return }

		msgID := wire.ID
		if msgID == "" { msgID = fmt.Sprintf("%d", time.Now().UnixNano()) }

		chatMgr.SaveMessage(from, chat.Message{
			ID: msgID, From: from, To: "me", Content: txt, Timestamp: time.Now(), Incoming: true, Status: "received",
		})
		w.Write([]byte("OK"))
	})

	// ... (Include other routes: /api/file/manifest, /api/file/query, /api/file/chunk, /api/feed/recv) ...
    // Note: Re-paste the routes from previous context if this file overwrites them.
    // For brevity I am ensuring the core logic change is above.

    // FILES
	mux.HandleFunc("/api/file/manifest", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { return }
		var payload ManifestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil { return }
		origin := Sanitize(payload.Owner)
		if origin == "" { return }
		if payload.Filter != nil { fileMgr.ProcessRemoteFilter(origin, payload.Filter) }
		w.Write([]byte("OK"))
	})

	mux.HandleFunc("/api/file/query", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" { return }
		results := fileMgr.LocalQuery(q)
		json.NewEncoder(w).Encode(results)
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
		from := Sanitize(wireMsg.From)
		if from == "" || pm.IsBlocked(from) { return }
		msgID := fmt.Sprintf("%d-%s", time.Now().UnixNano(), wireMsg.Nonce)
		msg := chat.Message{ID: msgID, From: from, To: "MESH", Content: wireMsg.Content, Timestamp: time.Now()}
		chatMgr.SaveFeedMessage(msg)
		w.Write([]byte("OK"))
	})
}

func setupPrivateAPIs(mux *http.ServeMux, pm *discovery.PeerManager, chatMgr *chat.Manager, fileMgr *files.Manager, torCtrl *tor.Controller, profileMgr *identity.ProfileManager, identityKey ed25519.PrivateKey) {
    // ... (Same as before) ...
	mux.HandleFunc("/api/peers", func(w http.ResponseWriter, r *http.Request) {
		addr := ""
		if torCtrl.Onion != nil { addr = torCtrl.Onion.ID + ".onion" }
		json.NewEncoder(w).Encode(map[string]interface{}{"self": addr, "peers": pm.GetPeers()})
	})

	mux.HandleFunc("/api/peers/add", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ OnionAddress string `json:"onion_address"` }
		json.NewDecoder(r.Body).Decode(&req)
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
		to := Sanitize(req.To)
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
		query := r.URL.Query().Get("q")
		if query == "" { return }
		candidates := fileMgr.SearchFilters(query)
		resultsChan := make(chan []files.FileMetadata, len(candidates))
		var wg sync.WaitGroup
		client, err := torCtrl.GetHttpClient()
		if err != nil { http.Error(w, "Tor not ready", 500); return }

		for _, peer := range candidates {
			wg.Add(1)
			go func(p string) {
				defer wg.Done()
				resp, err := client.Get(fmt.Sprintf("http://%s/api/file/query?q=%s", p, query))
				if err == nil && resp.StatusCode == 200 {
					var remoteFiles []files.FileMetadata
					if json.NewDecoder(resp.Body).Decode(&remoteFiles) == nil {
						for i := range remoteFiles { remoteFiles[i].Owner = p }
						resultsChan <- remoteFiles
					}
					resp.Body.Close()
				}
			}(peer)
		}
		go func() { wg.Wait(); close(resultsChan) }()
		var aggregated []files.FileMetadata
		for res := range resultsChan { aggregated = append(aggregated, res...) }
		json.NewEncoder(w).Encode(aggregated)
	})

	mux.HandleFunc("/api/files/download", func(w http.ResponseWriter, r *http.Request) {
		var req struct { PeerID, FileID, FileName string; Size int64 }
		json.NewDecoder(r.Body).Decode(&req)
		peerID := Sanitize(req.PeerID)
		if peerID == "" { return }
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