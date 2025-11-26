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

type PeerListResponse struct {
	Peers []string `json:"peers"`
}

type Message struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	Incoming  bool      `json:"incoming"`
}

// Conversation wrapper to track metadata
type Conversation struct {
	Messages   []Message
	Unread     bool
	LastActive time.Time
	Snippet    string // Short preview of last msg
}

type ChatStore struct {
	mu            sync.RWMutex
	Conversations map[string]*Conversation // Key: Peer Onion Address
}

// Initialize Store
var chatStore = ChatStore{
	Conversations: make(map[string]*Conversation),
}

// API Response for Chat Sidebar
type ChatStatus struct {
	PeerID     string    `json:"peer_id"`
	Unread     bool      `json:"unread"`
	LastActive time.Time `json:"last_active"`
	Snippet    string    `json:"snippet"`
}

// --- MAIN SERVER ---

func Start(port int, torCtrl *tor.Controller, pm *discovery.PeerManager) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("🖥️  AXON UI Ready at http://%s\n", addr)

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

	// 2. API: Get Peers
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

	// 3. API: Add Peer
	http.HandleFunc("/api/peers/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", 405)
			return
		}
		var req struct {
			OnionAddress string `json:"onion_address"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		rawAddr := sanitize(req.OnionAddress)
		if rawAddr == "" { return }
		if torCtrl.Onion != nil && rawAddr == torCtrl.Onion.ID + ".onion" { return }
		if pm.HasPeer(rawAddr) {
			http.Error(w, "Exists", 409)
			return
		}

		pm.AddPeer(rawAddr, "direct")
		go syncWithPeer(torCtrl, pm, rawAddr)
		w.Write([]byte(`{"status":"success"}`))
	})

	// 4. API: Incoming Handshake
	http.HandleFunc("/api/peers/announce", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { return }
		var req struct {
			OnionAddress string `json:"onion_address"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		cleanAddr := sanitize(req.OnionAddress)
		if cleanAddr == "" { return }

		fmt.Printf("👋 Incoming Handshake from: %s\n", cleanAddr)
		pm.AddPeer(cleanAddr, "neighbor")

		knownPeers := pm.GetPeers()
		var peerList []string
		for _, p := range knownPeers {
			peerList = append(peerList, p.OnionAddress)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PeerListResponse{Peers: peerList})
	})

	// --- CHAT APIs ---

	// 5. API: Get Chat History (Marks as Read)
	http.HandleFunc("/api/chat/history", func(w http.ResponseWriter, r *http.Request) {
		peer := r.URL.Query().Get("peer")
		chatStore.mu.Lock() // Lock for write (to clear unread)
		defer chatStore.mu.Unlock()

		conv, exists := chatStore.Conversations[peer]
		if !exists {
			json.NewEncoder(w).Encode([]Message{})
			return
		}

		// Mark as read
		conv.Unread = false

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(conv.Messages)
	})

// 6. API: Get Chat Status
	http.HandleFunc("/api/chat/status", func(w http.ResponseWriter, r *http.Request) {
		chatStore.mu.RLock()
		defer chatStore.mu.RUnlock()

		// CHANGE: Initialize as empty slice, not nil
		statusList := []ChatStatus{}

		for peerID, conv := range chatStore.Conversations {
			statusList = append(statusList, ChatStatus{
				PeerID:     peerID,
				Unread:     conv.Unread,
				LastActive: conv.LastActive,
				Snippet:    conv.Snippet,
			})
		}

		sort.Slice(statusList, func(i, j int) bool {
			return statusList[i].LastActive.After(statusList[j].LastActive)
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(statusList)
	})

	// 7. API: Send Message
	http.HandleFunc("/api/chat/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { return }
		var req struct {
			To      string `json:"to"`
			Content string `json:"content"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.To == "" || req.Content == "" { return }

		msg := Message{
			From:      "me",
			To:        req.To,
			Content:   req.Content,
			Timestamp: time.Now(),
			Incoming:  false,
		}

		// Update Store
		chatStore.mu.Lock()
		if _, ok := chatStore.Conversations[req.To]; !ok {
			chatStore.Conversations[req.To] = &Conversation{}
		}
		conv := chatStore.Conversations[req.To]
		conv.Messages = append(conv.Messages, msg)
		conv.LastActive = time.Now()
		conv.Snippet = "You: " + req.Content
		if len(conv.Snippet) > 30 { conv.Snippet = conv.Snippet[:27] + "..." }
		chatStore.mu.Unlock()

		// Send over Tor
		go func(target, content, me string) {
			if torCtrl.Onion == nil { return }
			client, err := torCtrl.GetHttpClient()
			if err != nil { return }

			outPayload := Message{
				From:      me + ".onion",
				Content:   content,
				Timestamp: time.Now(),
			}
			jsonBytes, _ := json.Marshal(outPayload)

			for i := 0; i < 3; i++ {
				resp, err := client.Post(
					fmt.Sprintf("http://%s/api/chat/recv", target),
					"application/json",
					bytes.NewBuffer(jsonBytes),
				)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode == 200 { return }
				}
				time.Sleep(5 * time.Second)
			}
			fmt.Printf("❌ Failed to deliver message to %s\n", target)
		}(req.To, req.Content, torCtrl.Onion.ID)

		w.Write([]byte(`{"status":"sent"}`))
	})

	// 8. API: Receive Message
	http.HandleFunc("/api/chat/recv", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { return }
		var msg Message
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil { return }

		from := sanitize(msg.From)
		if from == "" { return }

		msg.Incoming = true
		msg.From = from

		// Update Store & Mark Unread
		chatStore.mu.Lock()
		if _, ok := chatStore.Conversations[from]; !ok {
			chatStore.Conversations[from] = &Conversation{}
		}
		conv := chatStore.Conversations[from]
		conv.Messages = append(conv.Messages, msg)
		conv.LastActive = time.Now()
		conv.Unread = true // <--- FLAG UNREAD
		conv.Snippet = msg.Content
		if len(conv.Snippet) > 30 { conv.Snippet = conv.Snippet[:27] + "..." }
		chatStore.mu.Unlock()

		fmt.Printf("📩 Message received from %s\n", from)
		w.Write([]byte(`{"status":"received"}`))
	})

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("❌ Web UI failed to start: %v", err)
	}
}

// --- HELPERS ---

func sanitize(input string) string {
	s := strings.TrimSpace(input)
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
		if len(peers) == 0 { continue }
		randomIndex := rand.Intn(len(peers))
		target := peers[randomIndex]
		go syncWithPeer(torCtrl, pm, target.OnionAddress)
	}
}

func syncWithPeer(torCtrl *tor.Controller, pm *discovery.PeerManager, target string) {
	if torCtrl.Onion == nil { return }
	client, err := torCtrl.GetHttpClient()
	if err != nil { return }

	me := torCtrl.Onion.ID + ".onion"
	payload := map[string]string{"onion_address": me}
	jsonPayload, _ := json.Marshal(payload)

	for i := 1; i <= 3; i++ {
		resp, err := client.Post(
			fmt.Sprintf("http://%s/api/peers/announce", target),
			"application/json",
			bytes.NewBuffer(jsonPayload),
		)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				var gossip PeerListResponse
				if err := json.NewDecoder(resp.Body).Decode(&gossip); err == nil {
					for _, newPeer := range gossip.Peers {
						clean := sanitize(newPeer)
						if clean != me && !pm.HasPeer(clean) {
							fmt.Printf("💡 Learned about %s via %s\n", clean, target)
							pm.AddPeer(clean, "transitive")
						}
					}
				}
				return
			}
		}
		time.Sleep(10 * time.Second)
	}
}