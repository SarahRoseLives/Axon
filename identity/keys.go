package identity

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type KeyData struct {
	Type       string `json:"type"`
	PrivateKey string `json:"private_key"`
}

// LoadOrGenerateKey retrieves a key from disk or creates a new one
// Now accepts baseDir to ensure isolation between instances
func LoadOrGenerateKey(baseDir, name string) (ed25519.PrivateKey, error) {
	keyPath := filepath.Join(baseDir, name+".key")

	// 1. Try to load existing key
	if data, err := os.ReadFile(keyPath); err == nil {
		var k KeyData
		if err := json.Unmarshal(data, &k); err == nil {
			decoded, _ := base64.StdEncoding.DecodeString(k.PrivateKey)
			fmt.Printf("🔑 Loaded Identity: %s\n", name)
			return ed25519.PrivateKey(decoded), nil
		}
	}

	// 2. Generate new key if none exists
	fmt.Printf("🔨 Generating new Identity: %s...\n", name)
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}

	// 3. Save key to disk
	k := KeyData{
		Type:       "ed25519",
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
	}
	jsonData, _ := json.MarshalIndent(k, "", "  ")

	// Ensure dir exists (redundant safety check)
	os.MkdirAll(baseDir, 0700)

	err = os.WriteFile(keyPath, jsonData, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to save key: %w", err)
	}

	return priv, nil
}