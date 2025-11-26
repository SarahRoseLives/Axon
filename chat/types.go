package chat

import "time"

// WireMessage is the encrypted payload sent over HTTP
type WireMessage struct {
	From       string `json:"from"`
	Ciphertext string `json:"ciphertext"`
	Nonce      string `json:"nonce"`
}

// Message is the internal decrypted model
type Message struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	Incoming  bool      `json:"incoming"`
}

// Conversation tracks history and metadata
type Conversation struct {
	Messages   []Message `json:"messages"`
	Unread     bool      `json:"unread"`
	LastActive time.Time `json:"last_active"`
	Snippet    string    `json:"snippet"`
}

// ChatStatus is used for the UI Sidebar
type ChatStatus struct {
	PeerID     string    `json:"peer_id"`
	Unread     bool      `json:"unread"`
	LastActive time.Time `json:"last_active"`
	Snippet    string    `json:"snippet"`
}