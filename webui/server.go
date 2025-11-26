package webui

import (
	"axon/discovery"
	"axon/identity"
	"axon/tor"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	mrand "math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// --- DATA STRUCTURES ---

type UIContext struct {
	AppVersion string
	OnionAddr  string
	Status     string
	Peers      []discovery.Peer
}

type HandshakeRequest struct {
	OnionAddress string `json:"onion_address"`
	PublicKey    string `json:"public_key"`
}

type PeerListResponse struct {
	Peers     []string `json:"peers"`
	PublicKey string   `json:"public_key"`
}

type WireMessage struct {
	From       string `json:"from"`
	Ciphertext string `json:"ciphertext"`
	Nonce      string `json:"nonce"`
}

type Message struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	Incoming  bool      `json:"incoming"`
}

type Conversation struct {
	Messages   []Message `json:"messages"`
	Unread     bool      `json:"unread"`
	LastActive time.Time `json:"last_active"`
	Snippet    string    `json:"snippet"`
}

type ChatStore struct {
	mu            sync.RWMutex
	Conversations map[string]*Conversation
	dataDir       string
}

// --- GLOBALS ---
var (
	chatStore = ChatStore{Conversations: make(map[string]*Conversation)}
	myPrivKey *ecdh.PrivateKey
)

// --- MAIN SERVER ---

func Start(port int, torCtrl *tor.Controller, pm *discovery.PeerManager) {
	dataDir := fmt.Sprintf("data_%d", port)
	chatStore.dataDir = dataDir
	loadChatHistory()

	var err error
	myPrivKey, err = identity.LoadOrGenerateChatKey(dataDir)
	if err != nil {
		log.Fatalf("Failed to load chat key: %v", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("🖥️  AXON UI Ready at http://%s\n", addr)

	go startGossiping(torCtrl, pm)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		status := "Initializing..."
		onion := "Loading..."
		if torCtrl.Ready && torCtrl.Onion != nil {
			status = "Online"
			onion = torCtrl.Onion.ID + ".onion"
		}
		data := UIContext{
			AppVersion: "0.9.7-beta (Fixed Deadlock)",
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
		if torCtrl.Onion != nil { myAddr = torCtrl.Onion.ID + ".onion" }
		json.NewEncoder(w).Encode(map[string]interface{}{
			"self":  myAddr,
			"peers": pm.GetPeers(),
		})
	})

	http.HandleFunc("/api/peers/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { return }
		var req struct { OnionAddress string `json:"onion_address"` }
		json.NewDecoder(r.Body).Decode(&req)

		target := sanitize(req.OnionAddress)
		if target == "" { return }
		if torCtrl.Onion != nil && target == torCtrl.Onion.ID + ".onion" { return }

		if !pm.HasPeer(target) {
			pm.AddPeer(target, "direct", "")
		}

		go performHandshake(torCtrl, pm, target)
		w.Write([]byte(`{"status":"success"}`))
	})

	http.HandleFunc("/api/peers/announce", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { return }
		var req HandshakeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil { return }

		from := sanitize(req.OnionAddress)
		if from == "" { return }

		fmt.Printf("👋 Handshake from %s (Has Key: %v)\n", from, req.PublicKey != "")
		pm.AddPeer(from, "neighbor", req.PublicKey)

		knownPeers := pm.GetPeers()
		var peerList []string
		for _, p := range knownPeers {
			peerList = append(peerList, p.OnionAddress)
		}

		myPubKey := hex.EncodeToString(myPrivKey.PublicKey().Bytes())

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PeerListResponse{
			Peers:     peerList,
			PublicKey: myPubKey,
		})
	})

	// --- FIXED DEADLOCK HERE ---
	http.HandleFunc("/api/chat/history", func(w http.ResponseWriter, r *http.Request) {
		peer := r.URL.Query().Get("peer")

		chatStore.mu.Lock()
		conv, exists := chatStore.Conversations[peer]
		if exists {
			conv.Unread = false
			// Use INTERNAL save to avoid double-locking deadlock
			saveChatHistoryInternal()
		}
		chatStore.mu.Unlock()

		msgs := []Message{}
		if exists { msgs = conv.Messages }
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(msgs)
	})

	http.HandleFunc("/api/chat/status", func(w http.ResponseWriter, r *http.Request) {
		chatStore.mu.RLock()
		defer chatStore.mu.RUnlock()

		type ChatStatus struct {
			PeerID     string    `json:"peer_id"`
			Unread     bool      `json:"unread"`
			LastActive time.Time `json:"last_active"`
			Snippet    string    `json:"snippet"`
		}
		list := []ChatStatus{}
		for id, c := range chatStore.Conversations {
			list = append(list, ChatStatus{
				PeerID: id, Unread: c.Unread, LastActive: c.LastActive, Snippet: c.Snippet,
			})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].LastActive.After(list[j].LastActive) })
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(list)
	})

	http.HandleFunc("/api/chat/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { return }
		var req struct { To string `json:"to"`; Content string `json:"content"` }
		json.NewDecoder(r.Body).Decode(&req)

		if req.To == "" || req.Content == "" { return }

		peerKeyHex := pm.GetPublicKey(req.To)
		if peerKeyHex == "" {
			go performHandshake(torCtrl, pm, req.To)
			http.Error(w, "Peer encryption key missing. Handshaking... Try again in 5s.", 500)
			return
		}

		ciphertext, nonce, err := encryptMessage(peerKeyHex, req.Content)
		if err != nil {
			http.Error(w, "Encryption failed", 500)
			return
		}

		msg := Message{From: "me", To: req.To, Content: req.Content, Timestamp: time.Now(), Incoming: false}
		saveMessage(req.To, msg)

		go func() {
			if torCtrl.Onion == nil { return }
			client, err := torCtrl.GetHttpClient()
			if err != nil { return }

			wireMsg := WireMessage{
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
					if resp.StatusCode == 200 { return }
				}
				time.Sleep(5 * time.Second)
			}
			fmt.Printf("❌ Failed to send to %s\n", req.To)
		}()

		w.Write([]byte(`{"status":"sent"}`))
	})

	http.HandleFunc("/api/chat/recv", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { return }
		var wireMsg WireMessage
		if err := json.NewDecoder(r.Body).Decode(&wireMsg); err != nil { return }

		from := sanitize(wireMsg.From)
		peerKeyHex := pm.GetPublicKey(from)
		if peerKeyHex == "" {
			fmt.Printf("⚠️ Received msg from %s but missing key. Dropping & Handshaking.\n", from)
			go performHandshake(torCtrl, pm, from)
			return
		}

		plaintext, err := decryptMessage(peerKeyHex, wireMsg.Ciphertext, wireMsg.Nonce)
		if err != nil {
			fmt.Printf("⚠️ Decryption failed from %s: %v\n", from, err)
			return
		}

		msg := Message{From: from, To: "me", Content: plaintext, Timestamp: time.Now(), Incoming: true}
		saveMessage(from, msg)

		fmt.Printf("📩 Encrypted msg received from %s\n", from)
		w.Write([]byte(`{"status":"received"}`))
	})

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("❌ Web UI failed to start: %v", err)
	}
}

// --- LOGIC HELPERS ---

func saveMessage(peerID string, msg Message) {
	chatStore.mu.Lock()
	defer chatStore.mu.Unlock()

	if _, ok := chatStore.Conversations[peerID]; !ok {
		chatStore.Conversations[peerID] = &Conversation{}
	}
	conv := chatStore.Conversations[peerID]
	conv.Messages = append(conv.Messages, msg)
	conv.LastActive = time.Now()

	preview := msg.Content
	if len(preview) > 30 { preview = preview[:27] + "..." }
	if msg.Incoming {
		conv.Unread = true
		conv.Snippet = preview
	} else {
		conv.Snippet = "You: " + preview
	}

	saveChatHistoryInternal()
}

func performHandshake(torCtrl *tor.Controller, pm *discovery.PeerManager, target string) {
	if torCtrl.Onion == nil { return }
	client, err := torCtrl.GetHttpClient()
	if err != nil { return }

	myPubKey := hex.EncodeToString(myPrivKey.PublicKey().Bytes())
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

					if response.PublicKey != "" {
						pm.AddPeer(target, "direct", response.PublicKey)
					}

					for _, p := range response.Peers {
						clean := sanitize(p)
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

func loadChatHistory() {
	path := filepath.Join(chatStore.dataDir, "chats.json")
	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, &chatStore.Conversations)
		fmt.Printf("📂 Loaded history for %d chats\n", len(chatStore.Conversations))
	}
}

// This function locks
func saveChatHistory() {
	chatStore.mu.Lock()
	defer chatStore.mu.Unlock()
	saveChatHistoryInternal()
}

// This function DOES NOT lock (safe to call when you already have the lock)
func saveChatHistoryInternal() {
	path := filepath.Join(chatStore.dataDir, "chats.json")
	data, _ := json.MarshalIndent(chatStore.Conversations, "", "  ")
	os.WriteFile(path, data, 0600)
}

func encryptMessage(peerPubKeyHex, plaintext string) (string, string, error) {
	peerBytes, err := hex.DecodeString(peerPubKeyHex)
	if err != nil { return "", "", err }
	peerKey, err := ecdh.X25519().NewPublicKey(peerBytes)
	if err != nil { return "", "", err }

	sharedSecret, err := myPrivKey.ECDH(peerKey)
	if err != nil { return "", "", err }

	block, _ := aes.NewCipher(sharedSecret)
	gcm, _ := cipher.NewGCM(block)

	nonce := make([]byte, gcm.NonceSize())
	io.ReadFull(rand.Reader, nonce)

	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), hex.EncodeToString(nonce), nil
}

func decryptMessage(peerPubKeyHex, ciphertextHex, nonceHex string) (string, error) {
	peerBytes, _ := hex.DecodeString(peerPubKeyHex)
	peerKey, _ := ecdh.X25519().NewPublicKey(peerBytes)
	sharedSecret, _ := myPrivKey.ECDH(peerKey)

	block, _ := aes.NewCipher(sharedSecret)
	gcm, _ := cipher.NewGCM(block)

	nonce, _ := hex.DecodeString(nonceHex)
	ciphertext, _ := hex.DecodeString(ciphertextHex)

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil { return "", err }
	return string(plaintext), nil
}

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimSuffix(s, "/")
	return s
}

func startGossiping(torCtrl *tor.Controller, pm *discovery.PeerManager) {
	time.Sleep(30 * time.Second)
	for {
		time.Sleep(60 * time.Second)
		if torCtrl.Onion == nil { continue }
		peers := pm.GetPeers()
		if len(peers) > 0 {
			target := peers[mrand.Intn(len(peers))].OnionAddress
			go performHandshake(torCtrl, pm, target)
		}
	}
}