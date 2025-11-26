package chat

import (
	"axon/identity"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Manager struct {
	mu            sync.RWMutex
	Conversations map[string]*Conversation
	DataDir       string
	PrivKey       *ecdh.PrivateKey
}

func NewManager(dataDir string) (*Manager, error) {
	privKey, err := identity.LoadOrGenerateChatKey(dataDir)
	if err != nil {
		return nil, err
	}
	mgr := &Manager{
		Conversations: make(map[string]*Conversation),
		DataDir:       dataDir,
		PrivKey:       privKey,
	}
	mgr.loadHistory()
	return mgr, nil
}

// --- MESSAGE LOGIC ---

func (m *Manager) SaveMessage(peerID string, msg Message) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.Conversations[peerID]; !ok {
		m.Conversations[peerID] = &Conversation{}
	}
	conv := m.Conversations[peerID]

	// Default status if missing
	if msg.Status == "" {
		if msg.Incoming {
			msg.Status = "received"
		} else {
			msg.Status = "pending"
		}
	}

	conv.Messages = append(conv.Messages, msg)
	conv.LastActive = time.Now()

	preview := msg.Content
	if len(preview) > 30 {
		preview = preview[:27] + "..."
	}
	if msg.Incoming {
		conv.Unread = true
		conv.Snippet = preview
	} else {
		conv.Snippet = "You: " + preview
	}

	m.saveInternal()
}

func (m *Manager) UpdateMessageStatus(peerID, msgID, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	conv, exists := m.Conversations[peerID]
	if !exists { return }

	// Find and update
	for i, msg := range conv.Messages {
		if msg.ID == msgID {
			conv.Messages[i].Status = status
			m.saveInternal()
			return
		}
	}
}

func (m *Manager) GetPendingMessages() map[string][]Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pending := make(map[string][]Message)
	for peerID, conv := range m.Conversations {
		for _, msg := range conv.Messages {
			if !msg.Incoming && msg.Status == "pending" {
				pending[peerID] = append(pending[peerID], msg)
			}
		}
	}
	return pending
}

func (m *Manager) GetHistory(peerID string) []Message {
	m.mu.Lock()
	defer m.mu.Unlock()

	conv, exists := m.Conversations[peerID]
	if exists {
		conv.Unread = false
		m.saveInternal()
		return conv.Messages
	}
	return []Message{}
}

func (m *Manager) GetStatusList() []ChatStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var list []ChatStatus
	for id, c := range m.Conversations {
		list = append(list, ChatStatus{
			PeerID:     id,
			Unread:     c.Unread,
			LastActive: c.LastActive,
			Snippet:    c.Snippet,
		})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].LastActive.After(list[j].LastActive)
	})
	return list
}

// --- PERSISTENCE ---

func (m *Manager) loadHistory() {
	path := filepath.Join(m.DataDir, "chats.json")
	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, &m.Conversations)
		fmt.Printf("📂 Loaded history for %d chats\n", len(m.Conversations))
	}
}

func (m *Manager) saveInternal() {
	path := filepath.Join(m.DataDir, "chats.json")
	data, _ := json.MarshalIndent(m.Conversations, "", "  ")
	os.WriteFile(path, data, 0600)
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
	peerBytes, _ := hex.DecodeString(peerPubKeyHex)
	peerKey, _ := ecdh.X25519().NewPublicKey(peerBytes)
	sharedSecret, _ := m.PrivKey.ECDH(peerKey)

	block, _ := aes.NewCipher(sharedSecret)
	gcm, _ := cipher.NewGCM(block)

	nonce, _ := hex.DecodeString(nonceHex)
	ciphertext, _ := hex.DecodeString(ciphertextHex)

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil { return "", err }
	return string(plaintext), nil
}