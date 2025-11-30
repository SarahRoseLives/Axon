package files

import (
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "time"
)

const ChunkSize = 512 * 1024

// --- DATA STRUCTURES ---

type FileMetadata struct {
    ID          string    `json:"id"`
    Name        string    `json:"name"`
    Size        int64     `json:"size"`
    Type        string    `json:"type"`
    Owner       string    `json:"owner"`
    LastUpdated time.Time `json:"last_updated"`
    ChunkHashes []string  `json:"chunk_hashes"`
}

// TransferState tracks persistence on disk for resumable downloads
type TransferState struct {
    FileID      string          `json:"file_id"`
    Name        string          `json:"name"`
    PeerID      string          `json:"peer_id"` // The initial peer (primary)
    TotalChunks int             `json:"total_chunks"`
    DoneChunks  map[int]bool    `json:"done_chunks"` // Bitmask of completed parts
    LastUpdated time.Time       `json:"last_updated"`
}

// TransferStatus is the UI representation
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
    LocalFilter     *BloomFilter
    RemoteFilters   map[string]*BloomFilter

    // Maps FileID -> Persistence State
    Downloads       map[string]*TransferState

    // ActiveTransfers for UI status (derived from Downloads usually, but kept for compatibility logic)
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
        Downloads:       make(map[string]*TransferState),
        ActiveTransfers: make(map[string]*TransferStatus),
    }

    // Load persisted states to enable resume
    fm.loadResumeStates()

    fm.ScanSharedFolder()
    return fm
}

// --- STATE MANAGEMENT (RESUME LOGIC) ---

func (m *Manager) statePath(fileName string) string {
    return filepath.Join(m.DataDir, "downloads", fileName+".axon_state")
}

func (m *Manager) loadResumeStates() {
    dlDir := filepath.Join(m.DataDir, "downloads")
    files, _ := os.ReadDir(dlDir)

    for _, f := range files {
        if strings.HasSuffix(f.Name(), ".axon_state") {
            data, err := os.ReadFile(filepath.Join(dlDir, f.Name()))
            if err == nil {
                var state TransferState
                if json.Unmarshal(data, &state) == nil {
                    m.Downloads[state.FileID] = &state
                    fmt.Printf("🔄 Loaded resume state for: %s (%d/%d chunks)\n", state.Name, len(state.DoneChunks), state.TotalChunks)
                }
            }
        }
    }
}

func (m *Manager) saveState(state *TransferState) {
    data, _ := json.MarshalIndent(state, "", "  ")
    os.WriteFile(m.statePath(state.Name), data, 0644)
}

func (m *Manager) deleteState(fileName string) {
    os.Remove(m.statePath(fileName))
}

// --- DOWNLOAD LOGIC ---

func (m *Manager) RegisterDownload(fileID, name, peerID string, totalChunks int) {
    m.transferMu.Lock()
    defer m.transferMu.Unlock()

    // Check if state exists
    if state, exists := m.Downloads[fileID]; exists {
        state.PeerID = peerID // Update primary peer
        return
    }

    // Create new state
    newState := &TransferState{
        FileID:      fileID,
        Name:        name,
        PeerID:      peerID,
        TotalChunks: totalChunks,
        DoneChunks:  make(map[int]bool),
        LastUpdated: time.Now(),
    }
    m.Downloads[fileID] = newState
    m.saveState(newState)
}

func (m *Manager) IsChunkComplete(fileID string, idx int) bool {
    m.transferMu.RLock()
    defer m.transferMu.RUnlock()
    if state, ok := m.Downloads[fileID]; ok {
        return state.DoneChunks[idx]
    }
    return false
}

func (m *Manager) WriteChunk(fileID, fileName string, chunkIndex int, totalChunks int, data []byte) error {
    m.transferMu.Lock()
    state, ok := m.Downloads[fileID]
    if !ok {
        // Recover state if missing
        state = &TransferState{FileID: fileID, Name: fileName, TotalChunks: totalChunks, DoneChunks: make(map[int]bool)}
        m.Downloads[fileID] = state
    }
    m.transferMu.Unlock()

    // 1. Write Data
    dlPath := filepath.Join(m.DataDir, "downloads", fileName)
    f, err := os.OpenFile(dlPath, os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil { return err }

    offset := int64(chunkIndex * ChunkSize)
    _, err = f.Seek(offset, 0)
    if err == nil {
        _, err = f.Write(data)
    }
    f.Close()

    if err != nil { return err }

    // 2. Update State
    m.transferMu.Lock()
    state.DoneChunks[chunkIndex] = true
    state.LastUpdated = time.Now()
    isComplete := len(state.DoneChunks) >= state.TotalChunks
    m.transferMu.Unlock()

    // 3. Persist or Clean up
    if isComplete {
        fmt.Printf("🎉 Download Finalized: %s\n", fileName)
        m.deleteState(fileName)
    } else {
        // Only save periodically in high-performance apps, but for Tor, saving every chunk is fine safety.
        m.saveState(state)
    }

    return nil
}

// --- SWARM LOGIC ---

// GetOwners scans Bloom Filters to find all peers possessing the file
func (m *Manager) GetOwners(filename string) []string {
    m.indexMu.RLock()
    defer m.indexMu.RUnlock()

    var owners []string
    for peerID, filter := range m.RemoteFilters {
        if filter.Test(filename) {
            owners = append(owners, peerID)
        }
    }
    return owners
}

// --- UI HELPERS ---

func (m *Manager) GetTransfers() []TransferStatus {
    m.transferMu.RLock()
    defer m.transferMu.RUnlock()

    var list []TransferStatus
    for _, state := range m.Downloads {
        status := "active"
        if len(state.DoneChunks) >= state.TotalChunks {
            status = "completed"
        } else if time.Since(state.LastUpdated) > 1*time.Minute {
            status = "paused"
        }

        prog := 0.0
        if state.TotalChunks > 0 {
            prog = float64(len(state.DoneChunks)) / float64(state.TotalChunks)
        }

        list = append(list, TransferStatus{
            FileID:      state.FileID,
            Name:        state.Name,
            Direction:   "download",
            PeerID:      state.PeerID, // Shows primary peer
            Status:      status,
            TotalChunks: state.TotalChunks,
            SentChunks:  len(state.DoneChunks),
            Progress:    prog,
        })
    }
    return list
}

// --- STANDARD METHODS ---

func (m *Manager) ScanSharedFolder() {
    files, _ := os.ReadDir(m.SharedDir)
    newMap := make(map[string]FileMetadata)
    newFilter := NewBloomFilter()

    for _, f := range files {
        if f.IsDir() { continue }
        info, _ := f.Info()
        hashStr := fmt.Sprintf("%s-%d", info.Name(), info.Size())
        hasher := sha256.New()
        hasher.Write([]byte(hashStr))
        id := hex.EncodeToString(hasher.Sum(nil))[:16]

        newFilter.Add(info.Name())
        newMap[id] = FileMetadata{
            ID: id, Name: info.Name(), Size: info.Size(), Type: filepath.Ext(info.Name()),
            Owner: "me", LastUpdated: time.Now(),
        }
    }

    m.indexMu.Lock()
    m.LocalFiles = newMap
    m.LocalFilter = newFilter
    m.indexMu.Unlock()
}

func (m *Manager) GetLocalManifest() *BloomFilter {
    m.indexMu.RLock()
    defer m.indexMu.RUnlock()
    return m.LocalFilter
}

func (m *Manager) ProcessRemoteFilter(ownerID string, filter *BloomFilter) {
    m.indexMu.Lock()
    defer m.indexMu.Unlock()
    m.RemoteFilters[strings.TrimSpace(strings.ToLower(ownerID))] = filter
}

func (m *Manager) SearchFilters(query string) []string {
    m.indexMu.RLock()
    defer m.indexMu.RUnlock()
    var candidates []string
    for peerID, filter := range m.RemoteFilters {
        if filter.Test(query) { candidates = append(candidates, peerID) }
    }
    return candidates
}

func (m *Manager) LocalQuery(query string) []FileMetadata {
    m.indexMu.RLock()
    defer m.indexMu.RUnlock()
    query = strings.ToLower(strings.TrimSpace(query))
    var results []FileMetadata
    for _, f := range m.LocalFiles {
        if strings.Contains(strings.ToLower(f.Name), query) {
            f.Owner = "me"
            results = append(results, f)
        }
    }
    return results
}

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