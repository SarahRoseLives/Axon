package identity

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type KeyData struct {
	Type       string `json:"type"`
	PrivateKey string `json:"private_key"`
}

// 1. ONION IDENTITY (Ed25519) - For Tor Routing
func LoadOrGenerateKey(baseDir, name string) (ed25519.PrivateKey, error) {
	keyPath := filepath.Join(baseDir, name+".key")

	if data, err := os.ReadFile(keyPath); err == nil {
		var k KeyData
		if err := json.Unmarshal(data, &k); err == nil {
			decoded, _ := base64.StdEncoding.DecodeString(k.PrivateKey)
			fmt.Printf("🔑 Loaded Identity: %s\n", name)
			return ed25519.PrivateKey(decoded), nil
		}
	}

	fmt.Printf("🔨 Generating new Identity: %s...\n", name)
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}

	saveKey(keyPath, "ed25519", base64.StdEncoding.EncodeToString(priv))
	return priv, nil
}

// 2. CHAT ENCRYPTION KEY (X25519) - For E2EE
func LoadOrGenerateChatKey(baseDir string) (*ecdh.PrivateKey, error) {
	keyPath := filepath.Join(baseDir, "chat_encryption.key")

	if data, err := os.ReadFile(keyPath); err == nil {
		var k KeyData
		if err := json.Unmarshal(data, &k); err == nil {
			bytes, _ := hex.DecodeString(k.PrivateKey)
			priv, err := ecdh.X25519().NewPrivateKey(bytes)
			if err == nil {
				fmt.Printf("🔐 Loaded Chat Encryption Key\n")
				return priv, nil
			}
		}
	}

	fmt.Println("🛡️  Generating new Chat Encryption Key (X25519)...")
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	saveKey(keyPath, "x25519", hex.EncodeToString(priv.Bytes()))
	return priv, nil
}

func saveKey(path, typeStr, privStr string) {
	k := KeyData{Type: typeStr, PrivateKey: privStr}
	data, _ := json.MarshalIndent(k, "", "  ")
	os.WriteFile(path, data, 0600)
}