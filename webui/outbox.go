package webui

import (
    "axon/chat"
    "axon/discovery"
    "axon/files"
    "axon/tor"
    "bytes"
    "crypto/ed25519"
    "encoding/json"
    "fmt"
    "math"
    "sync"
    "time"
)

var TorLimiter = make(chan struct{}, 5)

func StartOutboxLoop(torCtrl *tor.Controller, pm *discovery.PeerManager, chatMgr *chat.Manager, fileMgr *files.Manager, getNickname func() string, identityKey ed25519.PrivateKey) {
    go func() {
        for {
            time.Sleep(200 * time.Millisecond)
            if torCtrl.Onion == nil { continue }

            pendingMap := chatMgr.GetPendingMessages()
            if len(pendingMap) > 0 {
                for peerID, msgs := range pendingMap {
                    if len(msgs) > 0 {
                        go func(pid string, m chat.Message) {
                            TorLimiter <- struct{}{}
                            AttemptSendMessage(torCtrl, pm, chatMgr, pid, m, getNickname(), identityKey)
                            <-TorLimiter
                        }(peerID, msgs[0])
                    }
                }
            }
        }
    }()
}

// BroadcastToAll sends our Bloom Filter to all neighbors
func BroadcastToAll(torCtrl *tor.Controller, pm *discovery.PeerManager, filter *files.BloomFilter) {
    peers := pm.GetPeers()
    if len(peers) == 0 { return }
    fmt.Printf("📢 Broadcasting Bloom Filter to %d neighbors...\n", len(peers))

    for _, p := range peers {
        go func(target string) {
            TorLimiter <- struct{}{}
            BroadcastManifest(torCtrl, target, filter, "")
            <-TorLimiter
        }(p.OnionAddress)
    }
}

// ForwardGossip propagates a Bloom Filter update to others
func ForwardGossip(torCtrl *tor.Controller, pm *discovery.PeerManager, origin string, filter *files.BloomFilter) {
    peers := pm.GetPeers()
    for _, p := range peers {
        if p.OnionAddress == origin { continue }
        go func(target string) {
            TorLimiter <- struct{}{}
            BroadcastManifest(torCtrl, target, filter, origin)
            <-TorLimiter
        }(p.OnionAddress)
    }
}

func BroadcastManifest(torCtrl *tor.Controller, target string, filter *files.BloomFilter, forceOwner string) {
    if filter == nil { return }

    client, err := torCtrl.GetHttpClient()
    if err != nil { return }

    owner := torCtrl.Onion.ID + ".onion"
    if forceOwner != "" {
        owner = forceOwner
    }

    payload := ManifestPayload{
        Owner:  owner,
        Filter: filter,
    }

    jsonBytes, _ := json.Marshal(payload)

    resp, err := client.Post(
        fmt.Sprintf("http://%s/api/file/manifest", target),
        "application/json",
        bytes.NewBuffer(jsonBytes),
    )

    if err == nil { resp.Body.Close() }
}

// PerformDownload - SWARM ENABLED & RESUMABLE
func PerformDownload(torCtrl *tor.Controller, fileMgr *files.Manager, primaryPeer, fileID, fileName string, size int64) {
    if fileID == "" { return }

    // 1. Swarm Discovery
    // Start with the peer we know has it, then ask the Manager who else matches the Bloom Filter
    swarm := []string{primaryPeer}
    potentialOwners := fileMgr.GetOwners(fileName)

    // Dedup map
    seen := map[string]bool{primaryPeer: true}
    for _, p := range potentialOwners {
        if !seen[p] && p != "" {
            swarm = append(swarm, p)
            seen[p] = true
        }
    }

    chunkSize := int64(files.ChunkSize)
    totalChunks := int(math.Ceil(float64(size) / float64(chunkSize)))

    // Initialize State (Load from disk if resume)
    fileMgr.RegisterDownload(fileID, fileName, primaryPeer, totalChunks)
    fmt.Printf("🐝 Starting Swarm Download: %s\n", fileName)
    fmt.Printf("👥 Swarm Size: %d peers %v\n", len(swarm), swarm)

    // 2. Job Queue Setup
    // Buffered channel to hold all needed chunks
    jobs := make(chan int, totalChunks)

    // Fill queue only with missing chunks (Resume Logic)
    chunksNeeded := 0
    for i := 0; i < totalChunks; i++ {
        if !fileMgr.IsChunkComplete(fileID, i) {
            jobs <- i
            chunksNeeded++
        }
    }

    if chunksNeeded == 0 {
        fmt.Printf("🎉 Download %s is already complete.\n", fileName)
        return
    }

    // 3. Worker Pool Setup
    var wg sync.WaitGroup
    client, err := torCtrl.GetHttpClient()
    if err != nil { return }

    // Spawn one worker per peer in the swarm
    for _, peer := range swarm {
        wg.Add(1)
        go func(p string) {
            defer wg.Done()

            // Consume jobs
            for chunkIdx := range jobs {
                // Double check if done (race condition safeguard)
                if fileMgr.IsChunkComplete(fileID, chunkIdx) {
                    continue
                }

                // Rate Limit
                TorLimiter <- struct{}{}

                reqURL := fmt.Sprintf("http://%s/api/file/chunk?id=%s&idx=%d", p, fileID, chunkIdx)
                resp, err := client.Get(reqURL)

                var data []byte
                if err == nil {
                    buf := new(bytes.Buffer)
                    buf.ReadFrom(resp.Body)
                    resp.Body.Close()
                    data = buf.Bytes()
                }

                // Release Token
                <-TorLimiter

                // Validation
                if err != nil || len(data) == 0 {
                    fmt.Printf("⚠️ Peer %s failed chunk %d: %v\n", p, chunkIdx, err)

                    // RE-QUEUE STRATEGY
                    // Put the job back for another worker to pick up
                    go func() { jobs <- chunkIdx }()

                    // Sleep briefly to avoid hammering a failing peer
                    time.Sleep(2 * time.Second)
                    continue
                }

                // Write to Disk
                err = fileMgr.WriteChunk(fileID, fileName, chunkIdx, totalChunks, data)
                if err != nil {
                    fmt.Printf("❌ Disk Error: %v\n", err)
                    // If disk fails, we probably can't continue safely
                    return
                }
            }
        }(peer)
    }

    // 4. Completion Monitor
    // We need to close the 'jobs' channel when all chunks are physically written to disk.
    // Since workers re-queue failed jobs, we can't just close 'jobs' when empty.
    // We poll the file manager status.

    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    for range ticker.C {
        allDone := true
        for i := 0; i < totalChunks; i++ {
            if !fileMgr.IsChunkComplete(fileID, i) {
                allDone = false
                break
            }
        }

        if allDone {
            close(jobs) // Signal workers to exit
            break
        }
    }

    wg.Wait()
    fmt.Printf("🎉 Swarm Download Complete: %s\n", fileName)
}

func AttemptSendMessage(torCtrl *tor.Controller, pm *discovery.PeerManager, chatMgr *chat.Manager, targetPeer string, msg chat.Message, myNickname string, privKey ed25519.PrivateKey) bool {
    peerKeyHex := pm.GetPublicKey(targetPeer)
    if peerKeyHex == "" {
        go func() {
            TorLimiter <- struct{}{}
            PerformHandshake(torCtrl, pm, targetPeer, chatMgr.GetMyPublicKey(), nil, myNickname, privKey)
            <-TorLimiter
        }()
        return false
    }

    ciphertext, nonce, err := chatMgr.Encrypt(peerKeyHex, msg.Content)
    if err != nil { return false }

    wireMsg := chat.WireMessage{
        ID:         msg.ID,
        From:       torCtrl.Onion.ID + ".onion",
        Ciphertext: ciphertext,
        Nonce:      nonce,
    }
    jsonBytes, _ := json.Marshal(wireMsg)

    client, err := torCtrl.GetHttpClient()
    if err != nil { return false }

    resp, err := client.Post(fmt.Sprintf("http://%s/api/chat/recv", targetPeer), "application/json", bytes.NewBuffer(jsonBytes))
    if err == nil {
        defer resp.Body.Close()
        if resp.StatusCode == 200 {
            chatMgr.UpdateMessageStatus(targetPeer, msg.ID, "sent")
            return true
        }
    }
    return false
}

func AttemptSendFeedMessage(torCtrl *tor.Controller, pm *discovery.PeerManager, chatMgr *chat.Manager, content, myNickname string) {
    if torCtrl.Onion == nil { return }

    client, err := torCtrl.GetHttpClient()
    if err != nil { return }

    msgID := fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Unix())
    wireMsg := chat.WireFeedMessage{From: torCtrl.Onion.ID + ".onion", Content: content, Nonce: msgID}
    jsonBytes, _ := json.Marshal(wireMsg)

    peers := pm.GetPeers()
    for _, peer := range peers {
        go func(target string) {
            TorLimiter <- struct{}{}
            resp, err := client.Post(fmt.Sprintf("http://%s/api/feed/recv", target), "application/json", bytes.NewBuffer(jsonBytes))
            if err == nil { resp.Body.Close() }
            <-TorLimiter
        }(peer.OnionAddress)
    }

    localMsg := chat.Message{ID: msgID, From: torCtrl.Onion.ID + ".onion", To: "MESH", Content: content, Timestamp: time.Now()}
    chatMgr.SaveFeedMessage(localMsg)
}