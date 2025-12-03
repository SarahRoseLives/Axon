package chat

import (
	"axon/database"
	"axon/identity"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
)

type Manager struct {
	DataDir string
	PrivKey *ecdh.PrivateKey
}

func NewManager(dataDir string) (*Manager, error) {
	privKey, err := identity.LoadOrGenerateChatKey(dataDir)
	if err != nil {
		return nil, err
	}

	return &Manager{
		DataDir: dataDir,
		PrivKey: privKey,
	}, nil
}

// --- MANAGEMENT ---

func (m *Manager) DeleteConversation(peerID string) {
	_, err := database.DB.Exec("DELETE FROM messages WHERE peer_id = ?", peerID)
	if err != nil {
		fmt.Printf("❌ Error deleting chat %s: %v\n", peerID, err)
	} else {
		fmt.Printf("🗑️ Deleted history with %s\n", peerID)
	}
}

// --- MESSAGE LOGIC ---

func (m *Manager) SaveMessage(targetID string, msg Message) {
	direction := "out"
	if msg.Incoming {
		direction = "in"
	}

	if msg.Status == "" {
		if msg.Incoming { msg.Status = "received" } else { msg.Status = "pending" }
	}

	query := `
		INSERT OR IGNORE INTO messages (id, peer_id, direction, content, status, timestamp)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	_, err := database.DB.Exec(query, msg.ID, targetID, direction, msg.Content, msg.Status, msg.Timestamp)

	if err != nil {
		fmt.Printf("❌ DB Write Error: %v\n", err)
	}
}

func (m *Manager) SaveFeedMessage(msg Message) {
	msg.Incoming = true
	msg.Status = "received"

	query := `
		INSERT OR IGNORE INTO messages (id, peer_id, direction, content, status, timestamp)
		VALUES (?, ?, 'feed', ?, 'received', ?)
	`
	database.DB.Exec(query, msg.ID, msg.From, msg.Content, msg.Timestamp)
}

func (m *Manager) UpdateMessageStatus(targetID, msgID, status string) {
	query := "UPDATE messages SET status = ? WHERE id = ?"
	database.DB.Exec(query, status, msgID)
}

func (m *Manager) GetPendingMessages() map[string][]Message {
	rows, err := database.DB.Query(`
		SELECT id, peer_id, content, timestamp
		FROM messages
		WHERE direction = 'out' AND status = 'pending'
	`)
	if err != nil { return nil }
	defer rows.Close()

	pending := make(map[string][]Message)

	for rows.Next() {
		var msg Message
		var peerID string
		rows.Scan(&msg.ID, &peerID, &msg.Content, &msg.Timestamp)

		msg.From = "me"
		msg.To = peerID
		msg.Status = "pending"
		msg.Incoming = false

		pending[peerID] = append(pending[peerID], msg)
	}
	return pending
}

func (m *Manager) GetHistory(peerID string) []Message {
    database.DB.Exec("UPDATE messages SET status = 'read' WHERE peer_id = ? AND direction = 'in' AND status = 'received'", peerID)

    rows, err := database.DB.Query(`
        SELECT id, direction, content, status, timestamp
        FROM messages
        WHERE peer_id = ?
        ORDER BY timestamp ASC LIMIT 50
    `, peerID)

    if err != nil { return []Message{} }
    defer rows.Close()

    var history []Message
    for rows.Next() {
        var m Message
        var dir string

        // --- FIX BELOW ---
        // Was: rows.Scan(&m.ID, &m.From, &m.Content, &m.Status, &m.Timestamp)
        // Now: Scan into '&dir' so the logic below works
        rows.Scan(&m.ID, &dir, &m.Content, &m.Status, &m.Timestamp)
        // -----------------

        if dir == "in" {
            m.Incoming = true
            m.From = peerID
            m.To = "me"
        } else {
            m.Incoming = false
            m.From = "me"
            m.To = peerID
        }
        history = append(history, m)
    }
    return history
}

func (m *Manager) GetFeedHistory() []Message {
	rows, err := database.DB.Query(`
		SELECT id, peer_id, content, timestamp
		FROM messages
		WHERE direction = 'feed'
		ORDER BY timestamp DESC LIMIT 50
	`)
	if err != nil { return []Message{} }
	defer rows.Close()

	var feed []Message
	for rows.Next() {
		var m Message
		rows.Scan(&m.ID, &m.From, &m.Content, &m.Timestamp)
		m.To = "MESH"
		feed = append(feed, m)
	}
	return feed
}

func (m *Manager) GetStatusList() []ChatStatus {
	rows, err := database.DB.Query("SELECT DISTINCT peer_id FROM messages WHERE direction != 'feed'")
	if err != nil { return []ChatStatus{} }
	defer rows.Close()

	var list []ChatStatus

	for rows.Next() {
		var pid string
		rows.Scan(&pid)

		var status ChatStatus
		status.PeerID = pid

		var content string
		err = database.DB.QueryRow(`
			SELECT content, timestamp
			FROM messages
			WHERE peer_id = ?
			ORDER BY timestamp DESC LIMIT 1
		`, pid).Scan(&content, &status.LastActive)

		if len(content) > 30 { content = content[:27] + "..." }
		status.Snippet = content

		var count int
		database.DB.QueryRow(`
			SELECT COUNT(*) FROM messages
			WHERE peer_id = ? AND direction = 'in' AND status = 'received'
		`, pid).Scan(&count)

		status.Unread = count > 0
		list = append(list, status)
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].LastActive.After(list[j].LastActive)
	})

	return list
}

// --- CRYPTO ---

func (m *Manager) GetMyPublicKey() string {
	return hex.EncodeToString(m.PrivKey.PublicKey().Bytes())
}

func (m *Manager) Encrypt(peerPubKeyHex, plaintext string) (string, string, error) {
	peerBytes, err := hex.DecodeString(peerPubKeyHex)
	if err != nil { return "", "", err }
	peerKey, err := ecdh.X25519().NewPublicKey(peerBytes)
	if err != nil { return "", "", err }

	sharedSecret, err := m.PrivKey.ECDH(peerKey)
	if err != nil { return "", "", err }

	block, _ := aes.NewCipher(sharedSecret)
	gcm, _ := cipher.NewGCM(block)

	nonce := make([]byte, gcm.NonceSize())
	io.ReadFull(rand.Reader, nonce)

	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), hex.EncodeToString(nonce), nil
}

func (m *Manager) Decrypt(peerPubKeyHex, ciphertextHex, nonceHex string) (string, error) {
	// 1. Decode Hex
	peerBytes, err := hex.DecodeString(peerPubKeyHex)
	if err != nil { return "", fmt.Errorf("bad peer key hex: %v", err) }

	nonce, err := hex.DecodeString(nonceHex)
	if err != nil { return "", fmt.Errorf("bad nonce hex: %v", err) }

	ciphertext, err := hex.DecodeString(ciphertextHex)
	if err != nil { return "", fmt.Errorf("bad ciphertext hex: %v", err) }

	// DEBUG LOGGING
	fmt.Printf("\n🔐 [DEBUG DECRYPT]\n")
	fmt.Printf("   - Ciphertext Len: %d\n", len(ciphertext))
	fmt.Printf("   - Nonce Len:      %d (Expected: 12)\n", len(nonce))

	// 2. Generate Shared Secret
	peerKey, err := ecdh.X25519().NewPublicKey(peerBytes)
	if err != nil { return "", fmt.Errorf("invalid peer public key curve point") }

	sharedSecret, _ := m.PrivKey.ECDH(peerKey)

	// 3. Init AES-GCM
	block, _ := aes.NewCipher(sharedSecret)
	gcm, _ := cipher.NewGCM(block)

	// 4. Decrypt
    // Go's gcm.Open expects 'ciphertext' to contain the MAC at the end.
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
        // Common error: "cipher: message authentication failed"
		return "", fmt.Errorf("gcm open failed: %w", err)
	}
	return string(plaintext), nil
}