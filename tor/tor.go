package tor

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"net/http"
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

// Start initializes Tor, creates the service, AND starts the HTTP server on it
func (c *Controller) Start(baseDir string, privKey ed25519.PrivateKey) (string, error) {
	fmt.Println("🌱 Initializing Tor Background Service...")

	torDataDir := filepath.Join(baseDir, "tor_sys")
	if err := os.MkdirAll(torDataDir, 0700); err != nil {
		return "", fmt.Errorf("could not create data dir: %w", err)
	}

	t, err := tor.Start(nil, &tor.StartConf{DataDir: torDataDir})
	if err != nil {
		return "", fmt.Errorf("tor start failed: %w", err)
	}
	c.Instance = t

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

	// ---------------------------------------------------------
	// 🔥 CRITICAL FIX: Bind the HTTP Server to the Tor Listener
	// ---------------------------------------------------------
	// passing 'nil' uses the DefaultServeMux, which is where
	// webui/server.go registered all its routes.
	go http.Serve(onion, nil)

	return onion.ID, nil
}

func (c *Controller) GetHttpClient() (*http.Client, error) {
	if !c.Ready || c.Instance == nil {
		return nil, fmt.Errorf("tor not ready")
	}

	dialer, err := c.Instance.Dialer(context.Background(), nil)
	if err != nil {
		return nil, err
	}

	return &http.Client{
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
		},
		Timeout: 180 * time.Second,
	}, nil
}

func (c *Controller) Stop() {
	if c.Instance != nil {
		c.Instance.Close()
	}
}