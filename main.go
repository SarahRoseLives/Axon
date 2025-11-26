package main

import (
	"axon/discovery" // <--- Import this
	"axon/identity"
	"axon/tor"
	"axon/webui"
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	// 1. Setup
	portPtr := flag.Int("port", 8080, "Web UI Port")
	flag.Parse()
	port := *portPtr

	dataDir := fmt.Sprintf("data_%d", port)
	cwd, _ := os.Getwd()
	fmt.Printf("🚀 Starting AXON Node on port %d\n", port)
	fmt.Printf("📂 Data Directory: %s/%s\n", cwd, dataDir)

	os.MkdirAll(dataDir, 0700)

	// 2. Identity
	privKey, err := identity.LoadOrGenerateKey(dataDir, "axon_identity")
	if err != nil {
		log.Fatalf("Fatal: Could not load identity: %v", err)
	}

	// 3. Peer Manager (NEW)
	// Initialize the list of known friends
	peerManager := discovery.NewPeerManager(dataDir)

	// 4. Tor
	torInstance := tor.NewController()
	go func() {
		onionAddr, err := torInstance.Start(dataDir, privKey)
		if err != nil {
			log.Printf("❌ Tor Setup Failed: %v", err)
			return
		}
		fmt.Printf("\n🧅 ONION SERVICE LIVE: %s.onion\n", onionAddr)
	}()

	// 5. Start Web UI
	// Pass the peerManager to the UI so it can display them
	webui.Start(port, torInstance, peerManager)
}