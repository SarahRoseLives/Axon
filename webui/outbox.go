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

// StartOutboxLoop periodically checks for pending messages and tries to send them
func StartOutboxLoop(torCtrl *tor.Controller, pm *discovery.PeerManager, chatMgr *chat.Manager) {
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
					fmt.Printf("   -> Retrying msg to %s\n", peerID)
					AttemptSendMessage(torCtrl, pm, chatMgr, peerID, msg)
				}
			}
		}
	}()
}

// AttemptSendMessage tries to encrypt and send a message over Tor.
// Returns true if successful.
func AttemptSendMessage(torCtrl *tor.Controller, pm *discovery.PeerManager, chatMgr *chat.Manager, targetPeer string, msg chat.Message) bool {

	// 1. Get Key
	peerKeyHex := pm.GetPublicKey(targetPeer)
	if peerKeyHex == "" {
		// Try to handshake so next time it might work
		go PerformHandshake(torCtrl, pm, targetPeer, chatMgr.GetMyPublicKey())
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