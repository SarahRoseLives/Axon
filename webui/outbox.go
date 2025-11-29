package webui

import (
    "axon/chat"
    "axon/discovery"
    "axon/files"
    "axon/tor"
    "bytes"
    "crypto/ed25519" // Added
    "encoding/json"
    "fmt"
    "math"
    "time"
)

var TorLimiter = make(chan struct{}, 3)

// Updated Signature to include Identity Key
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

func BroadcastToAll(torCtrl *tor.Controller, pm *discovery.PeerManager, localFiles []files.FileMetadata) {
    peers := pm.GetPeers()
    if len(peers) == 0 { return }
    fmt.Printf("📢 Broadcasting manifest to %d neighbors...\n", len(peers))

    for _, p := range peers {
        go func(target string) {
            TorLimiter <- struct{}{}
            BroadcastManifest(torCtrl, target, localFiles, "")
            <-TorLimiter
        }(p.OnionAddress)
    }
}

func ForwardGossip(torCtrl *tor.Controller, pm *discovery.PeerManager, origin string, manifest []files.FileMetadata) {
    peers := pm.GetPeers()
    for _, p := range peers {
        if p.OnionAddress == origin { continue }
        go func(target string) {
            TorLimiter <- struct{}{}
            BroadcastManifest(torCtrl, target, manifest, origin)
            <-TorLimiter
        }(p.OnionAddress)
    }
}

func BroadcastManifest(torCtrl *tor.Controller, target string, manifest []files.FileMetadata, forceOwner string) {
    client, err := torCtrl.GetHttpClient()
    if err != nil { return }

    finalManifest := make([]files.FileMetadata, len(manifest))
    myAddr := torCtrl.Onion.ID + ".onion"

    for i, f := range manifest {
        if forceOwner == "" {
            f.Owner = myAddr
        } else {
            f.Owner = forceOwner
        }
        finalManifest[i] = f
    }

    payload, _ := json.Marshal(finalManifest)
    resp, err := client.Post(
        fmt.Sprintf("http://%s/api/file/manifest", target),
        "application/json",
        bytes.NewBuffer(payload),
    )

    if err == nil { resp.Body.Close() }
}

func PerformDownload(torCtrl *tor.Controller, fileMgr *files.Manager, targetPeer, fileID, fileName string, size int64) {
    if targetPeer == "" || fileID == "" { return }

    client, err := torCtrl.GetHttpClient()
    if err != nil { return }

    chunkSize := int64(files.ChunkSize)
    totalChunks := int(math.Ceil(float64(size) / float64(chunkSize)))

    fileMgr.RegisterDownload(fileID, fileName, targetPeer, totalChunks)
    fmt.Printf("⬇️ Queueing Download: %s from %s (%d chunks)\n", fileName, targetPeer, totalChunks)

    for i := 0; i < totalChunks; i++ {
        TorLimiter <- struct{}{}

        reqURL := fmt.Sprintf("http://%s/api/file/chunk?id=%s&idx=%d", targetPeer, fileID, i)
        resp, err := client.Get(reqURL)

        var data []byte
        if err == nil {
            buf := new(bytes.Buffer)
            buf.ReadFrom(resp.Body)
            resp.Body.Close()
            data = buf.Bytes()
        }
        <-TorLimiter

        if err != nil {
            fmt.Printf("❌ Chunk %d failed: %v\n", i, err)
            time.Sleep(3 * time.Second)
            i--
            continue
        }

        if len(data) < 100 && len(data) > 0 && data[0] == '{' {
            fmt.Println("❌ Error from peer:", string(data))
            return
        }

        err = fileMgr.WriteChunk(fileID, fileName, i, totalChunks, data)
        if err != nil {
            fmt.Printf("❌ Disk Write error: %v\n", err)
            return
        }
        time.Sleep(100 * time.Millisecond)
    }
    fmt.Printf("🎉 Download Complete: %s\n", fileName)
}

// Updated signature + PerformHandshake call
func AttemptSendMessage(torCtrl *tor.Controller, pm *discovery.PeerManager, chatMgr *chat.Manager, targetPeer string, msg chat.Message, myNickname string, privKey ed25519.PrivateKey) bool {
    peerKeyHex := pm.GetPublicKey(targetPeer)
    if peerKeyHex == "" {
        go func() {
            TorLimiter <- struct{}{}
            // Pass nil for fileMgr to avoid re-syncing files on just a chat connection retry
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