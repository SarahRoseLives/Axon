package chat

import "time"

type WireMessage struct {
    ID         string `json:"id"`
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
    Status    string    `json:"status"`
    From      string    `json:"from"`
    To        string    `json:"to"`
    Content   string    `json:"content"`
    Timestamp time.Time `json:"timestamp"`
    Incoming  bool      `json:"incoming"`
}

type Conversation struct {
    // Used only for legacy JSON, not needed for SQL but kept for compatibility if needed
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