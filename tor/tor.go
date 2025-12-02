package tor

import (
	"context"
	"crypto/ed25519"
	"crypto/tls" // <--- Added for TLS Config
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cretz/bine/tor"
)

type Controller struct {
	Instance   *tor.Tor
	Onion      *tor.OnionService
	Ready      bool
	httpClient *http.Client
	clientMu   sync.Mutex
}

func NewController() *Controller {
	return &Controller{Ready: false}
}

// Start initializes Tor and returns the OnionService listener directly.
// We listen on Port 443 so incoming traffic from Flutter (which forces HTTPS) works correctly.
func (c *Controller) Start(baseDir string, privKey ed25519.PrivateKey) (*tor.OnionService, error) {
	fmt.Println("🧅 Initializing Tor Background Service...")

	torDataDir := filepath.Join(baseDir, "tor_sys")
	if err := os.MkdirAll(torDataDir, 0700); err != nil {
		return nil, fmt.Errorf("could not create data dir: %w", err)
	}

	conf := &tor.StartConf{DataDir: torDataDir}

	// Start Tor process
	t, err := tor.Start(nil, conf)
	if err != nil {
		return nil, fmt.Errorf("tor start failed: %w", err)
	}
	c.Instance = t

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	fmt.Println("⏳ Establishing Circuit (this may take 30s)...")

	// LISTEN ON PORT 443 (HTTPS)
	// Flutter's HttpClient will send 'CONNECT <onion>:443' when we use https:// links.
	// Tor will route that to this listener.
	onion, err := t.Listen(ctx, &tor.ListenConf{
		Version3:    true,
		RemotePorts: []int{443},
		Key:         privKey,
	})
	if err != nil {
		t.Close()
		return nil, fmt.Errorf("onion service creation failed: %w", err)
	}

	c.Onion = onion
	c.Ready = true

	// Return the listener so server.go can wrap it in TLS
	return onion, nil
}

// GetHttpClient returns a client capable of routing through Tor.
// CRITICAL FIX: It ignores self-signed certificate errors.
func (c *Controller) GetHttpClient() (*http.Client, error) {
	c.clientMu.Lock()
	defer c.clientMu.Unlock()

	if !c.Ready || c.Instance == nil {
		return nil, fmt.Errorf("tor not ready")
	}

	// Return cached client if it exists
	if c.httpClient != nil {
		return c.httpClient, nil
	}

	// Create the Tor Dialer
	dialer, err := c.Instance.Dialer(context.Background(), nil)
	if err != nil {
		return nil, err
	}

	// Configure Transport
	transport := &http.Transport{
		DialContext:         dialer.DialContext,
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     90 * time.Second,

		// --- CRITICAL FIX FOR HANDSHAKE ERRORS ---
		// We use self-signed certs for all nodes.
		// This tells Go to accept the connection anyway.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	c.httpClient = &http.Client{
		Transport: transport,
		Timeout:   180 * time.Second,
	}

	return c.httpClient, nil
}

func (c *Controller) Stop() {
	if c.Instance != nil {
		c.Instance.Close()
	}
}