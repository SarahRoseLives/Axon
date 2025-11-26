package tor

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cretz/bine/tor"
)

type Controller struct {
	Instance *tor.Tor
	Onion    *tor.OnionService
	Ready    bool
}

func NewController() *Controller {
	return &Controller{Ready: false}
}

// Start now takes baseDir to isolate Tor instances
func (c *Controller) Start(baseDir string, privKey ed25519.PrivateKey) (string, error) {
	fmt.Println("🌱 Initializing Tor Background Service...")

	// 1. Prepare Data Directory inside the specific port folder
	// e.g. data_8080/tor_sys
	torDataDir := filepath.Join(baseDir, "tor_sys")

	if err := os.MkdirAll(torDataDir, 0700); err != nil {
		return "", fmt.Errorf("could not create data dir: %w", err)
	}

	// 2. Start Tor Process
	t, err := tor.Start(nil, &tor.StartConf{
		DataDir: torDataDir,
		// DebugWriter: os.Stdout,
	})
	if err != nil {
		return "", fmt.Errorf("tor start failed: %w", err)
	}
	c.Instance = t

	// 3. Create Onion Service (V3)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	fmt.Println("⏳ Establishing Circuit (this may take 30s)...")
	onion, err := t.Listen(ctx, &tor.ListenConf{
		Version3:    true,
		RemotePorts: []int{80},
		Key:         privKey,
	})
	if err != nil {
		t.Close()
		return "", fmt.Errorf("onion service creation failed: %w", err)
	}

	c.Onion = onion
	c.Ready = true
	return onion.ID, nil
}

func (c *Controller) Stop() {
	if c.Instance != nil {
		c.Instance.Close()
	}
}