package main

import (
	"axon/discovery"
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

	// 2. Identity (Load Keys)
	// We need this Key here to pass it down to Tor via WebUI
	privKey, err := identity.LoadOrGenerateKey(dataDir, "axon_identity")
	if err != nil {
		log.Fatalf("Fatal: Could not load identity: %v", err)
	}

	// 3. Peer Manager
	peerManager := discovery.NewPeerManager(dataDir)

	// 4. Tor Controller
	// We create the controller, but we DO NOT start it here.
	// We pass it to WebUI, which will start it with the RESTRICTED Handler.
	torInstance := tor.NewController()
	defer torInstance.Stop()

	// 5. Start Web UI (and Tor Background Service)
	webui.Start(port, torInstance, peerManager, privKey)
}