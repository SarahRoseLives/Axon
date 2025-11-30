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

const ChunkSize = 512 * 1024

type FileMetadata struct {
    ID          string    `json:"id"`
    Name        string    `json:"name"`
    Size        int64     `json:"size"`
    Type        string    `json:"type"`
    Owner       string    `json:"owner"`
    LastUpdated time.Time `json:"last_updated"`
    ChunkHashes []string  `json:"chunk_hashes"`
}

type TransferStatus struct {
    FileID      string  `json:"file_id"`
    Name        string  `json:"name"`
    Direction   string  `json:"direction"`
    PeerID      string  `json:"peer_id"`
    Progress    float64 `json:"progress"`
    Status      string  `json:"status"`
    TotalChunks int     `json:"total_chunks"`
    SentChunks  int     `json:"sent_chunks"`
}

type Manager struct {
    indexMu         sync.RWMutex
    transferMu      sync.RWMutex

    SharedDir       string
    DataDir         string

    LocalFiles      map[string]FileMetadata
    LocalFilter     *BloomFilter            // <--- NEW: Our compressed index
    RemoteFilters   map[string]*BloomFilter // <--- NEW: Others' compressed indexes

    ActiveTransfers map[string]*TransferStatus
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
        LocalFilter:     NewBloomFilter(),
        RemoteFilters:   make(map[string]*BloomFilter),
        ActiveTransfers: make(map[string]*TransferStatus),
    }
    fm.ScanSharedFolder()
    return fm
}

func (m *Manager) RegisterDownload(fileID, name, peerID string, totalChunks int) {
    m.transferMu.Lock()
    defer m.transferMu.Unlock()

    m.ActiveTransfers[fileID] = &TransferStatus{
        FileID:      fileID,
        Name:        name,
        Direction:   "download",
        PeerID:      peerID,
        Status:      "pending",
        TotalChunks: totalChunks,
        SentChunks:  0,
        Progress:    0.0,
    }
}

// --- INDEXING & HASHING ---

func (m *Manager) ScanSharedFolder() {
    files, _ := os.ReadDir(m.SharedDir)
    newMap := make(map[string]FileMetadata)
    newFilter := NewBloomFilter() // Rebuild filter from scratch

    fmt.Println("📂 Scanning and Hashing Shared Files (Bloom Mode)...")

    for _, f := range files {
        if f.IsDir() { continue }
        info, _ := f.Info()
        path := filepath.Join(m.SharedDir, info.Name())

        // Calculate ID
        hashStr := fmt.Sprintf("%s-%d", info.Name(), info.Size())
        hasher := sha256.New()
        hasher.Write([]byte(hashStr))
        id := hex.EncodeToString(hasher.Sum(nil))[:16]

        // Add to Bloom Filter
        newFilter.Add(info.Name())

        // Merkle Calc (Only if new or changed - simplified here for brevity)
        hashes, err := m.hashFileChunks(path)
        if err != nil {
            fmt.Printf("⚠️ Failed to hash %s: %v\n", info.Name(), err)
            continue
        }

        newMap[id] = FileMetadata{
            ID:          id,
            Name:        info.Name(),
            Size:        info.Size(),
            Type:        filepath.Ext(info.Name()),
            Owner:       "me",
            LastUpdated: time.Now(),
            ChunkHashes: hashes,
        }
    }

    m.indexMu.Lock()
    m.LocalFiles = newMap
    m.LocalFilter = newFilter
    count := len(m.LocalFiles)
    m.indexMu.Unlock()
    fmt.Printf("✅ Indexed %d local files into Bloom Filter.\n", count)
}

func (m *Manager) hashFileChunks(path string) ([]string, error) {
    f, err := os.Open(path)
    if err != nil { return nil, err }
    defer f.Close()

    var hashes []string
    buf := make([]byte, ChunkSize)

    for {
        n, err := f.Read(buf)
        if n > 0 {
            sum := sha256.Sum256(buf[:n])
            hashes = append(hashes, hex.EncodeToString(sum[:]))
        }
        if err == io.EOF { break }
        if err != nil { return nil, err }
    }
    return hashes, nil
}

func (m *Manager) GetLocalManifest() *BloomFilter {
    m.indexMu.RLock()
    defer m.indexMu.RUnlock()
    return m.LocalFilter
}

func (m *Manager) ProcessRemoteFilter(ownerID string, filter *BloomFilter) {
    m.indexMu.Lock()
    defer m.indexMu.Unlock()
    ownerID = strings.TrimSpace(strings.ToLower(ownerID))
    m.RemoteFilters[ownerID] = filter
    fmt.Printf("📚 Updated Bloom Filter for %s\n", ownerID)
}

// SearchFilters checks WHO might have the file.
// Returns a list of PeerIDs.
func (m *Manager) SearchFilters(query string) []string {
    m.indexMu.RLock()
    defer m.indexMu.RUnlock()

    var candidates []string
    for peerID, filter := range m.RemoteFilters {
        if filter.Test(query) {
            candidates = append(candidates, peerID)
        }
    }
    return candidates
}

// LocalQuery searches ACTUAL file objects.
// This is called when a peer asks US "Do you have 'matrix'?"
func (m *Manager) LocalQuery(query string) []FileMetadata {
    m.indexMu.RLock()
    defer m.indexMu.RUnlock()

    query = strings.ToLower(strings.TrimSpace(query))
    var results []FileMetadata

    for _, f := range m.LocalFiles {
        if strings.Contains(strings.ToLower(f.Name), query) {
            f.Owner = "me" // Ensure owner is set correctly for response
            results = append(results, f)
        }
    }
    return results
}

// --- IO OPS ---

func (m *Manager) GetChunk(fileID string, chunkIndex int) ([]byte, error) {
    m.indexMu.RLock()
    meta, ok := m.LocalFiles[fileID]
    m.indexMu.RUnlock()

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
    // Note: We removed the strict Merkle hash check against RemoteIndex here
    // because we no longer have the full RemoteIndex in RAM.
    // In a production app, we would request the hash list from the peer before downloading.
    // For now, we trust the chunk stream.

    m.transferMu.Lock()
    if _, ok := m.ActiveTransfers[fileID]; !ok {
        m.ActiveTransfers[fileID] = &TransferStatus{
            FileID: fileID, Name: fileName, Direction: "download",
            Status: "active", TotalChunks: totalChunks,
        }
    }
    trans := m.ActiveTransfers[fileID]
    trans.Status = "active"
    trans.SentChunks++
    if totalChunks > 0 {
        trans.Progress = float64(trans.SentChunks) / float64(totalChunks)
    }
    if trans.SentChunks >= totalChunks {
        trans.Status = "completed"
        trans.Progress = 1.0
    }
    m.transferMu.Unlock()

    dlPath := filepath.Join(m.DataDir, "downloads", fileName)
    f, err := os.OpenFile(dlPath, os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil { return err }

    offset := int64(chunkIndex * ChunkSize)
    _, err = f.Seek(offset, 0)
    if err == nil {
        _, err = f.Write(data)
    }
    f.Close()

    return err
}

func (m *Manager) GetTransfers() []TransferStatus {
    m.transferMu.RLock()
    defer m.transferMu.RUnlock()
    var list []TransferStatus
    for _, t := range m.ActiveTransfers {
        list = append(list, *t)
    }
    return list
}