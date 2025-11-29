package main

import (
    "axon/database"
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

    // 2. INIT DATABASE (Critical for SQLite)
    database.Init(dataDir)

    // 3. Identity
    // We load the key into 'privKey'
    privKey, err := identity.LoadOrGenerateKey(dataDir, "axon_identity")
    if err != nil {
        log.Fatalf("Fatal: Could not load identity: %v", err)
    }

    // 4. Managers
    // We only need PeerManager here because it's passed explicitly.
    // Chat and File managers are initialized inside webui.Start to ensure they share the same context.
    peerManager := discovery.NewPeerManager(dataDir)

    // 5. Tor Controller
    torInstance := tor.NewController()
    defer torInstance.Stop()

    // 6. Start Web UI
    // FIX: Pass 'privKey' (the variable we declared), not 'identityKey' (which doesn't exist here)
    webui.Start(port, torInstance, peerManager, privKey)
}