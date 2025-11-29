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
	ChunkHashes []string  `json:"chunk_hashes"` // Merkle-like integrity
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
	indexMu        sync.RWMutex
	transferMu     sync.RWMutex

	SharedDir      string
	DataDir        string

	LocalFiles     map[string]FileMetadata
	RemoteIndex    map[string]map[string]FileMetadata
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
		RemoteIndex:     make(map[string]map[string]FileMetadata),
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

	fmt.Println("📂 Scanning and Hashing Shared Files (This may take a moment)...")

	for _, f := range files {
		if f.IsDir() { continue }
		info, _ := f.Info()
		path := filepath.Join(m.SharedDir, info.Name())

		// Calculate ID (Name+Size Hash)
		hashStr := fmt.Sprintf("%s-%d", info.Name(), info.Size())
		hasher := sha256.New()
		hasher.Write([]byte(hashStr))
		id := hex.EncodeToString(hasher.Sum(nil))[:16]

		// Calculate Chunk Hashes (Merkle Tree)
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
	count := len(m.LocalFiles)
	m.indexMu.Unlock()
	fmt.Printf("✅ Indexed %d local files with integrity checks.\n", count)
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

func (m *Manager) GetLocalManifest() []FileMetadata {
	m.indexMu.RLock()
	defer m.indexMu.RUnlock()
	list := make([]FileMetadata, 0, len(m.LocalFiles))
	for _, f := range m.LocalFiles {
		list = append(list, f)
	}
	return list
}

func (m *Manager) ProcessRemoteManifest(ownerID string, files []FileMetadata) bool {
	m.indexMu.Lock()
	defer m.indexMu.Unlock()

	ownerID = strings.TrimSpace(strings.ToLower(ownerID))
	if _, ok := m.RemoteIndex[ownerID]; !ok {
		m.RemoteIndex[ownerID] = make(map[string]FileMetadata)
	}

	changed := false
	currentMap := m.RemoteIndex[ownerID]

	for _, f := range files {
		f.Owner = ownerID
		existing, exists := currentMap[f.ID]
		if !exists || f.LastUpdated.After(existing.LastUpdated) {
			currentMap[f.ID] = f
			changed = true
		}
	}
	if changed {
		fmt.Printf("📚 Library Update: Learned %d files from %s\n", len(files), ownerID)
	}
	return changed
}

func (m *Manager) Search(query string) []FileMetadata {
	m.indexMu.RLock()
	defer m.indexMu.RUnlock()
	query = strings.ToLower(strings.TrimSpace(query))
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

// VERIFIED WRITE
func (m *Manager) WriteChunk(fileID, fileName string, chunkIndex int, totalChunks int, data []byte) error {
	// 1. Verify Integrity (Merkle Check)
	m.indexMu.RLock()
	// We need to find the remote file metadata to get the hash list
	// This is a bit expensive searching all peers, but necessary without a direct look up map
	// Optimization: Pass PeerID to WriteChunk to look up directly.
	// For now, we iterate (acceptable for small networks).
	var expectedHash string
	found := false
	for _, peerFiles := range m.RemoteIndex {
		if meta, ok := peerFiles[fileID]; ok {
			if chunkIndex < len(meta.ChunkHashes) {
				expectedHash = meta.ChunkHashes[chunkIndex]
				found = true
				break
			}
		}
	}
	m.indexMu.RUnlock()

	if found {
		sum := sha256.Sum256(data)
		actualHash := hex.EncodeToString(sum[:])
		if actualHash != expectedHash {
			return fmt.Errorf("integrity check failed: hash mismatch for chunk %d", chunkIndex)
		}
	}

	// 2. Update Status
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

	// 3. Perform Disk IO
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