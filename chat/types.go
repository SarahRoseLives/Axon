package chat

import "time"

type WireMessage struct {
    ID         string `json:"id"` // <--- NEW: Persist ID across network
    From       string `json:"from"`
    Ciphertext string `json:"ciphertext"`
    Nonce      string `json:"nonce"`
}

type WireFeedMessage struct {
    From    string `json:"from"`
    Content string `json:"content"`
    Nonce   string `json:"nonce"`
}

type Message struct {
    ID        string    `json:"id"`
    Status    string    `json:"status"` // pending, sent, received
    From      string    `json:"from"`
    To        string    `json:"to"`
    Content   string    `json:"content"`
    Timestamp time.Time `json:"timestamp"`
    Incoming  bool      `json:"incoming"`
}

type Conversation struct {
    Messages   []Message `json:"messages"`
    Unread     bool      `json:"unread"`
    LastActive time.Time `json:"last_active"`
    Snippet    string    `json:"snippet"`
}

type ChatStatus struct {
    PeerID     string    `json:"peer_id"`
    Unread     bool      `json:"unread"`
    LastActive time.Time `json:"last_active"`
    Snippet    string    `json:"snippet"`
}