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
    portPtr := flag.Int("port", 8080, "Web UI Port")
    flag.Parse()
    port := *portPtr

    dataDir := fmt.Sprintf("data_%d", port)
    cwd, _ := os.Getwd()
    fmt.Printf("🚀 Starting AXON Node on port %d\n", port)
    fmt.Printf("📂 Data Directory: %s/%s\n", cwd, dataDir)

    os.MkdirAll(dataDir, 0700)

    // Identity
    privKey, err := identity.LoadOrGenerateKey(dataDir, "axon_identity")
    if err != nil {
        log.Fatalf("Fatal: Could not load identity: %v", err)
    }

    // Managers
    peerManager := discovery.NewPeerManager(dataDir)
    torInstance := tor.NewController()
    defer torInstance.Stop()

    // Start UI & Services (This now starts Files internally)
    webui.Start(port, torInstance, peerManager, privKey)
}