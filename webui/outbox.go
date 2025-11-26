package webui

import (
	"axon/chat"
	"axon/discovery"
	"axon/tor"
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Updated: Now requires getNickname closure so retries always use latest name
func StartOutboxLoop(torCtrl *tor.Controller, pm *discovery.PeerManager, chatMgr *chat.Manager, getNickname func() string) {
	go func() {
		for {
			time.Sleep(60 * time.Second)

			if torCtrl.Onion == nil { continue }

			pendingMap := chatMgr.GetPendingMessages()
			if len(pendingMap) == 0 { continue }

			fmt.Printf("📮 Outbox: Processing %d conversations...\n", len(pendingMap))

			for peerID, msgs := range pendingMap {
				// We process only 1 message per peer per cycle to avoid congestion
				if len(msgs) > 0 {
					msg := msgs[0]
					fmt.Printf("  -> Retrying msg to %s\n", peerID)
					// Pass current nickname
					AttemptSendMessage(torCtrl, pm, chatMgr, peerID, msg, getNickname())
				}
			}
		}
	}()
}

// NEW: Send a public post to ALL known peers (gossip)
func AttemptSendFeedMessage(torCtrl *tor.Controller, pm *discovery.PeerManager, chatMgr *chat.Manager, content, myNickname string) {
	if torCtrl.Onion == nil { return }
	client, err := torCtrl.GetHttpClient()
	if err != nil { return }

	msgID := fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Unix())

	// We use a nonce to prevent a peer from re-posting our old message
	nonce := msgID

	// 1. Prepare Wire Payload (Public, plaintext)
	wireMsg := chat.WireFeedMessage{
		From:    torCtrl.Onion.ID + ".onion",
		Content: content,
		Nonce:   nonce,
	}
	jsonBytes, _ := json.Marshal(wireMsg)

	peers := pm.GetPeers()
	fmt.Printf("📢 Broadcasting feed post to %d peers...\n", len(peers))

	// Broadcast to all peers (non-blocking)
	for _, peer := range peers {
		targetPeer := peer.OnionAddress
		go func(target string) {
			resp, err := client.Post(
				fmt.Sprintf("http://%s/api/feed/recv", target),
				"application/json",
				bytes.NewBuffer(jsonBytes),
			)
			if err == nil && resp.StatusCode == 200 {
				fmt.Printf("✅ Delivered feed post to %s\n", target)
			} else if err != nil {
				fmt.Printf("❌ Failed to deliver feed post to %s: %v\n", target, err)
			}
			if resp != nil {
				resp.Body.Close()
			}
		}(targetPeer)
	}

	// Also save it locally (it's "incoming" from the "me" identity on this node)
	localMsg := chat.Message{
		ID:        msgID,
		From:      torCtrl.Onion.ID + ".onion",
		To:        "MESH", // Public post target
		Content:   content,
		Timestamp: time.Now(),
	}
	chatMgr.SaveFeedMessage(localMsg)
}

// Updated: Now accepts myNickname to pass to PerformHandshake if needed
func AttemptSendMessage(torCtrl *tor.Controller, pm *discovery.PeerManager, chatMgr *chat.Manager, targetPeer string, msg chat.Message, myNickname string) bool {

	// 1. Get Key
	peerKeyHex := pm.GetPublicKey(targetPeer)
	if peerKeyHex == "" {
		// Try to handshake so next time it might work
		// FIXED: Passing myNickname here
		go PerformHandshake(torCtrl, pm, targetPeer, chatMgr.GetMyPublicKey(), myNickname)
		return false
	}

	// 2. Encrypt
	ciphertext, nonce, err := chatMgr.Encrypt(peerKeyHex, msg.Content)
	if err != nil {
		fmt.Printf("❌ Encryption failed for %s: %v\n", targetPeer, err)
		return false
	}

	// 3. Prepare Wire Payload
	wireMsg := chat.WireMessage{
		From:       torCtrl.Onion.ID + ".onion",
		Ciphertext: ciphertext,
		Nonce:      nonce,
	}
	jsonBytes, _ := json.Marshal(wireMsg)

	// 4. Send (with short retry)
	client, err := torCtrl.GetHttpClient()
	if err != nil { return false }

	// Try once (the outbox loop handles long-term retries)
	resp, err := client.Post(
		fmt.Sprintf("http://%s/api/chat/recv", targetPeer),
		"application/json",
		bytes.NewBuffer(jsonBytes),
	)

	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			// SUCCESS! Update Status
			chatMgr.UpdateMessageStatus(targetPeer, msg.ID, "sent")
			fmt.Printf("✅ Delivered pending msg to %s\n", targetPeer)
			return true
		}
	}

	return false
}