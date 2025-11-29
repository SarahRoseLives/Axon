package tor

import (
    "context"
    "crypto/ed25519"
    "fmt"
    "net/http"
    "os"
    "path/filepath"
    "runtime"
    "sync"
    "time"

    "github.com/cretz/bine/tor"
)

type Controller struct {
    Instance   *tor.Tor
    Onion      *tor.OnionService
    Ready      bool
    httpClient *http.Client // CACHED CLIENT
    clientMu   sync.Mutex
}

func NewController() *Controller {
    return &Controller{Ready: false}
}

func (c *Controller) Start(baseDir string, privKey ed25519.PrivateKey, handler http.Handler) (string, error) {
    fmt.Println("🧅 Initializing Tor Background Service...")

    torDataDir := filepath.Join(baseDir, "tor_sys")
    if err := os.MkdirAll(torDataDir, 0700); err != nil {
        return "", fmt.Errorf("could not create data dir: %w", err)
    }

    conf := &tor.StartConf{DataDir: torDataDir}

    if runtime.GOOS == "windows" {
        if _, err := os.Stat("tor.exe"); err == nil {
            absPath, _ := filepath.Abs("tor.exe")
            conf.ExePath = absPath
        }
    }

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

    go http.Serve(onion, handler)

    return onion.ID, nil
}

// OPTIMIZED: Reuses the same client (and underlying TCP connections)
func (c *Controller) GetHttpClient() (*http.Client, error) {
    c.clientMu.Lock()
    defer c.clientMu.Unlock()

    if !c.Ready || c.Instance == nil {
        return nil, fmt.Errorf("tor not ready")
    }

    // Return existing client if available
    if c.httpClient != nil {
        return c.httpClient, nil
    }

    dialer, err := c.Instance.Dialer(context.Background(), nil)
    if err != nil {
        return nil, err
    }

    // Create a Transport that supports Keep-Alives
    transport := &http.Transport{
        DialContext:         dialer.DialContext,
        MaxIdleConns:        10,
        MaxIdleConnsPerHost: 5,
        IdleConnTimeout:     90 * time.Second,
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