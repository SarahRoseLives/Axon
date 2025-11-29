package files

import (
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "time"
)

// --- TYPES ---

type FileMetadata struct {
    ID          string    `json:"id"` // SHA256 Hash
    Name        string    `json:"name"`
    Size        int64     `json:"size"`
    Type        string    `json:"type"`
    Owner       string    `json:"owner"` // Onion Address
    LastUpdated time.Time `json:"last_updated"`
}

type TransferStatus struct {
    FileID      string  `json:"file_id"`
    Name        string  `json:"name"`
    Direction   string  `json:"direction"` // "upload" or "download"
    PeerID      string  `json:"peer_id"`
    Progress    float64 `json:"progress"` // 0.0 to 1.0
    Status      string  `json:"status"`   // "active", "completed", "failed"
    TotalChunks int     `json:"total_chunks"`
    SentChunks  int     `json:"sent_chunks"`
}

// --- MANAGER ---

type Manager struct {
    mu             sync.RWMutex
    SharedDir      string
    DataDir        string
    LocalFiles     map[string]FileMetadata            // My files
    RemoteIndex    map[string]map[string]FileMetadata // PeerID -> [FileID -> Meta]
    ActiveTransfers map[string]*TransferStatus        // Key: FileID
}

func NewManager(dataDir string) *Manager {
    shared := filepath.Join(dataDir, "shared")
    os.MkdirAll(shared, 0700)
    downloads := filepath.Join(dataDir, "downloads")
    os.MkdirAll(downloads, 0700)

    fm := &Manager{
        SharedDir:       shared,
        DataDir:         dataDir,
        LocalFiles:      make(map[string]FileMetadata),
        RemoteIndex:     make(map[string]map[string]FileMetadata),
        ActiveTransfers: make(map[string]*TransferStatus),
    }
    // Initial Scan
    fm.ScanSharedFolder()
    return fm
}

// --- INDEXING & SEARCH ---

func (m *Manager) ScanSharedFolder() {
    m.mu.Lock()
    defer m.mu.Unlock()

    files, _ := os.ReadDir(m.SharedDir)
    m.LocalFiles = make(map[string]FileMetadata) // Reset

    for _, f := range files {
        if f.IsDir() { continue }
        info, _ := f.Info()

        // Simple Hash: Name + Size (For speed, ideally read content)
        // For production, we'd hash the first 1KB + size
        hashStr := fmt.Sprintf("%s-%d", info.Name(), info.Size())
        hasher := sha256.New()
        hasher.Write([]byte(hashStr))
        id := hex.EncodeToString(hasher.Sum(nil))[:16]

        m.LocalFiles[id] = FileMetadata{
            ID:          id,
            Name:        info.Name(),
            Size:        info.Size(),
            Type:        filepath.Ext(info.Name()),
            Owner:       "me",
            LastUpdated: time.Now(),
        }
    }
    fmt.Printf("📂 Indexed %d local shared files\n", len(m.LocalFiles))
}

func (m *Manager) GetLocalManifest() []FileMetadata {
    m.mu.RLock()
    defer m.mu.RUnlock()
    list := make([]FileMetadata, 0, len(m.LocalFiles))
    for _, f := range m.LocalFiles {
        list = append(list, f)
    }
    return list
}

func (m *Manager) ProcessRemoteManifest(peerID string, files []FileMetadata) {
    m.mu.Lock()
    defer m.mu.Unlock()

    if _, ok := m.RemoteIndex[peerID]; !ok {
        m.RemoteIndex[peerID] = make(map[string]FileMetadata)
    }

    // Replace entries
    m.RemoteIndex[peerID] = make(map[string]FileMetadata)
    for _, f := range files {
        f.Owner = peerID // Enforce owner
        m.RemoteIndex[peerID][f.ID] = f
    }
    fmt.Printf("📚 Updated library: %s has %d files\n", peerID, len(files))
}

func (m *Manager) Search(query string) []FileMetadata {
    m.mu.RLock()
    defer m.mu.RUnlock()

    query = strings.ToLower(query)
    var results []FileMetadata

    for _, peerFiles := range m.RemoteIndex {
        for _, f := range peerFiles {
            if strings.Contains(strings.ToLower(f.Name), query) {
                results = append(results, f)
            }
        }
    }
    return results
}

// --- IO OPS ---

const ChunkSize = 64 * 1024 // 64KB chunks for Tor

func (m *Manager) GetChunk(fileID string, chunkIndex int) ([]byte, error) {
    m.mu.RLock()
    meta, ok := m.LocalFiles[fileID]
    m.mu.RUnlock()

    if !ok { return nil, fmt.Errorf("file not found") }

    path := filepath.Join(m.SharedDir, meta.Name)
    f, err := os.Open(path)
    if err != nil { return nil, err }
    defer f.Close()

    offset := int64(chunkIndex * ChunkSize)
    if offset >= meta.Size { return nil, io.EOF }

    buffer := make([]byte, ChunkSize)
    _, err = f.Seek(offset, 0)
    if err != nil { return nil, err }

    n, err := f.Read(buffer)
    if err != nil && err != io.EOF { return nil, err }

    return buffer[:n], nil
}

func (m *Manager) WriteChunk(fileID, fileName string, chunkIndex int, totalChunks int, data []byte) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    dlPath := filepath.Join(m.DataDir, "downloads", fileName)

    // Open file in Append/Write mode
    f, err := os.OpenFile(dlPath, os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil { return err }
    defer f.Close()

    offset := int64(chunkIndex * ChunkSize)
    _, err = f.Seek(offset, 0)
    if err != nil { return err }

    _, err = f.Write(data)

    // Update Transfer Status
    if _, ok := m.ActiveTransfers[fileID]; !ok {
        m.ActiveTransfers[fileID] = &TransferStatus{
            FileID: fileID, Name: fileName, Direction: "download",
            Status: "active", TotalChunks: totalChunks,
        }
    }
    trans := m.ActiveTransfers[fileID]
    trans.SentChunks++
    trans.Progress = float64(trans.SentChunks) / float64(totalChunks)
    if trans.SentChunks >= totalChunks {
        trans.Status = "completed"
        trans.Progress = 1.0
    }

    return err
}

func (m *Manager) GetTransfers() []TransferStatus {
    m.mu.RLock()
    defer m.mu.RUnlock()
    var list []TransferStatus
    for _, t := range m.ActiveTransfers {
        list = append(list, *t)
    }
    return list
}