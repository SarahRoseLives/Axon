package tor

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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

// UPDATED: Now accepts 'handler http.Handler' to serve specific routes over Tor
func (c *Controller) Start(baseDir string, privKey ed25519.PrivateKey, handler http.Handler) (string, error) {
	fmt.Println("🧅 Initializing Tor Background Service...")

	torDataDir := filepath.Join(baseDir, "tor_sys")
	if err := os.MkdirAll(torDataDir, 0700); err != nil {
		return "", fmt.Errorf("could not create data dir: %w", err)
	}

	// Create the configuration object
	conf := &tor.StartConf{
		DataDir: torDataDir,
	}

	// ---------------------------------------------------------
	// WINDOWS FIX: Use Absolute Path
	// ---------------------------------------------------------
	if runtime.GOOS == "windows" {
		if _, err := os.Stat("tor.exe"); err == nil {
			absPath, _ := filepath.Abs("tor.exe")
			fmt.Printf("🪟 Windows detected: Using local binary at %s\n", absPath)
			conf.ExePath = absPath
		} else {
			fmt.Println("⚠️ Warning: tor.exe not found in directory. Assuming it is in %PATH%...")
		}
	}

	// Start Tor with the config
	t, err := tor.Start(nil, conf)
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

	// SECURITY FIX: Bind the restricted handler (Public Mux) to the Tor Listener
	// Do NOT use 'nil' here, or it will use DefaultServeMux (the Admin UI)
	go http.Serve(onion, handler)

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