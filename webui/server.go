package webui

import (
	"axon/discovery"
	"axon/tor"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
)

// UIContext data for the HTML template
type UIContext struct {
	AppVersion string
	OnionAddr  string
	Status     string
	Peers      []discovery.Peer // <--- Now allows us to loop over peers in HTML
}

func Start(port int, torCtrl *tor.Controller, pm *discovery.PeerManager) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("🖥️  AXON UI Ready at http://%s\n", addr)

	// 1. Serve HTML
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
			Peers:      pm.GetPeers(), // <--- Inject real peers
		}

		tmpl, err := template.ParseGlob("webui/templates/*.html")
		if err != nil {
			http.Error(w, "Template Error: "+err.Error(), 500)
			return
		}
		err = tmpl.ExecuteTemplate(w, "index.html", data)
		if err != nil {
			// This is where your error came from.
			// We log it but don't panic.
			log.Printf("Template render error (usually browser disconnect): %v", err)
		}
	})

	// 2. API: Add Peer
	// This will be called when you click "Add User" in the UI (we'll wire that next)
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

		// Add as Direct Trust
		pm.AddPeer(req.OnionAddress, "direct")
		w.Write([]byte(`{"status":"success"}`))
	})

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("❌ Web UI failed to start: %v", err)
	}
}