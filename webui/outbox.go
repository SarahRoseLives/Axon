package webui

import (
    "axon/chat"
    "axon/discovery"
    "axon/files"
    "axon/tor"
    "bytes"
    "encoding/json"
    "fmt"
    "math"
    "sync"
    "time"
)

// Global Traffic Controller
var (
    NetworkMutex sync.Mutex
)

// StartOutboxLoop manages outgoing traffic priorities
func StartOutboxLoop(torCtrl *tor.Controller, pm *discovery.PeerManager, chatMgr *chat.Manager, fileMgr *files.Manager, getNickname func() string) {

    // 1. CHAT LOOP (High Priority)
    go func() {
        for {
            time.Sleep(200 * time.Millisecond)
            if torCtrl.Onion == nil { continue }

            pendingMap := chatMgr.GetPendingMessages()
            if len(pendingMap) > 0 {
                NetworkMutex.Lock()

                fmt.Printf("📮 Outbox: Processing %d conversations...\n", len(pendingMap))
                for peerID, msgs := range pendingMap {
                    if len(msgs) > 0 {
                        msg := msgs[0]
                        fmt.Printf("📨 Priority: Sending Chat to %s\n", peerID)
                        AttemptSendMessage(torCtrl, pm, chatMgr, peerID, msg, getNickname())
                    }
                }

                NetworkMutex.Unlock()
            }
        }
    }()

    // 2. GOSSIP LOOP (Medium Priority - File Manifests)
    go func() {
        time.Sleep(30 * time.Second) // Warmup
        for {
            time.Sleep(60 * time.Second) // Every minute
            if torCtrl.Onion == nil { continue }

            NetworkMutex.Lock()
            manifest := fileMgr.GetLocalManifest()

            if len(manifest) > 0 {
                peers := pm.GetPeers()
                if len(peers) > 0 {
                     fmt.Printf("📚 Gossiping file manifest (%d files) to %d peers...\n", len(manifest), len(peers))
                     for _, p := range peers {
                        go BroadcastManifest(torCtrl, p.OnionAddress, manifest)
                     }
                }
            }
            NetworkMutex.Unlock()
        }
    }()

    // 3. DOWNLOAD LOOP is handled via PerformDownload API calls
}

// --- NETWORK ACTIONS ---

func BroadcastManifest(torCtrl *tor.Controller, target string, localFiles []files.FileMetadata) {
    client, err := torCtrl.GetHttpClient()
    if err != nil {
        fmt.Printf("❌ Tor Client Error for %s: %v\n", target, err)
        return
    }

    // =========================================================
    // 🔧 FIX: STAMP THE MANIFEST WITH OUR ONION ADDRESS
    // =========================================================
    // We cannot send "Owner: me". We must send "Owner: <my-onion-address>"
    // We create a new slice so we don't mess up our local manager's data.
    myAddr := torCtrl.Onion.ID + ".onion"
    stampedFiles := make([]files.FileMetadata, len(localFiles))

    for i, f := range localFiles {
        f.Owner = myAddr // <--- The crucial fix
        stampedFiles[i] = f
    }

    payload, _ := json.Marshal(stampedFiles)
    resp, err := client.Post(
        fmt.Sprintf("http://%s/api/file/manifest", target),
        "application/json",
        bytes.NewBuffer(payload),
    )

    if err != nil {
        fmt.Printf("❌ Gossip failed to %s: %v\n", target, err)
        return
    }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        fmt.Printf("⚠️ Gossip rejected by %s (Status: %d)\n", target, resp.StatusCode)
    }
}

func PerformDownload(torCtrl *tor.Controller, fileMgr *files.Manager, targetPeer, fileID, fileName string, size int64) {
    client, err := torCtrl.GetHttpClient()
    if err != nil { return }

    chunkSize := int64(files.ChunkSize)
    totalChunks := int(math.Ceil(float64(size) / float64(chunkSize)))

    fmt.Printf("⬇️ Starting Download: %s (%d chunks) from %s\n", fileName, totalChunks, targetPeer)

    for i := 0; i < totalChunks; i++ {
        NetworkMutex.Lock()
        NetworkMutex.Unlock()

        reqURL := fmt.Sprintf("http://%s/api/file/chunk?id=%s&idx=%d", targetPeer, fileID, i)

        resp, err := client.Get(reqURL)
        if err != nil {
            fmt.Printf("❌ Chunk %d failed: %v\n", i, err)
            time.Sleep(5 * time.Second)
            i--
            continue
        }

        buf := new(bytes.Buffer)
        buf.ReadFrom(resp.Body)
        resp.Body.Close()

        data := buf.Bytes()

        if len(data) < 100 && len(data) > 0 && data[0] == '{' {
             fmt.Println("❌ Peer returned error:", string(data))
             return
        }

        err = fileMgr.WriteChunk(fileID, fileName, i, totalChunks, data)
        if err != nil {
            fmt.Printf("❌ Write error: %v\n", err)
            return
        }

        time.Sleep(50 * time.Millisecond)
    }
    fmt.Printf("🎉 Download Complete: %s\n", fileName)
}

func AttemptSendMessage(torCtrl *tor.Controller, pm *discovery.PeerManager, chatMgr *chat.Manager, targetPeer string, msg chat.Message, myNickname string) bool {
    peerKeyHex := pm.GetPublicKey(targetPeer)
    if peerKeyHex == "" {
        go PerformHandshake(torCtrl, pm, targetPeer, chatMgr.GetMyPublicKey(), myNickname)
        return false
    }

    ciphertext, nonce, err := chatMgr.Encrypt(peerKeyHex, msg.Content)
    if err != nil {
        fmt.Printf("❌ Encryption failed for %s: %v\n", targetPeer, err)
        return false
    }

    wireMsg := chat.WireMessage{
        From:       torCtrl.Onion.ID + ".onion",
        Ciphertext: ciphertext,
        Nonce:      nonce,
    }
    jsonBytes, _ := json.Marshal(wireMsg)

    client, err := torCtrl.GetHttpClient()
    if err != nil { return false }

    resp, err := client.Post(
        fmt.Sprintf("http://%s/api/chat/recv", targetPeer),
        "application/json",
        bytes.NewBuffer(jsonBytes),
    )

    if err == nil {
        defer resp.Body.Close()
        if resp.StatusCode == 200 {
            chatMgr.UpdateMessageStatus(targetPeer, msg.ID, "sent")
            fmt.Printf("✅ Delivered pending msg to %s\n", targetPeer)
            return true
        } else {
             fmt.Printf("⚠️ Delivery failed to %s: Status %d\n", targetPeer, resp.StatusCode)
        }
    } else {
        fmt.Printf("❌ Network error sending to %s: %v\n", targetPeer, err)
    }

    return false
}

func AttemptSendFeedMessage(torCtrl *tor.Controller, pm *discovery.PeerManager, chatMgr *chat.Manager, content, myNickname string) {
    if torCtrl.Onion == nil { return }

    NetworkMutex.Lock()
    defer NetworkMutex.Unlock()

    client, err := torCtrl.GetHttpClient()
    if err != nil { return }

    msgID := fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Unix())
    nonce := msgID

    wireMsg := chat.WireFeedMessage{
        From:    torCtrl.Onion.ID + ".onion",
        Content: content,
        Nonce:   nonce,
    }
    jsonBytes, _ := json.Marshal(wireMsg)

    peers := pm.GetPeers()
    fmt.Printf("📢 Broadcasting feed post to %d peers...\n", len(peers))

    for _, peer := range peers {
        targetPeer := peer.OnionAddress
        go func(target string) {
            resp, err := client.Post(
                fmt.Sprintf("http://%s/api/feed/recv", target),
                "application/json",
                bytes.NewBuffer(jsonBytes),
            )
            if err == nil && resp.StatusCode == 200 {
                // Success
            } else if err != nil {
                fmt.Printf("❌ Failed to broadcast to %s: %v\n", target, err)
            }
            if resp != nil {
                resp.Body.Close()
            }
        }(targetPeer)
    }

    localMsg := chat.Message{
        ID:        msgID,
        From:      torCtrl.Onion.ID + ".onion",
        To:        "MESH",
        Content:   content,
        Timestamp: time.Now(),
    }
    chatMgr.SaveFeedMessage(localMsg)
}