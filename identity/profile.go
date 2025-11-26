package identity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Profile struct {
	Nickname string `json:"nickname"`
}

type ProfileManager struct {
	mu      sync.RWMutex
	Profile Profile
	DataDir string
}

func NewProfileManager(dataDir string) *ProfileManager {
	pm := &ProfileManager{
		DataDir: dataDir,
		Profile: Profile{Nickname: "Anonymous"}, // Default
	}
	pm.load()
	return pm
}

func (pm *ProfileManager) GetNickname() string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.Profile.Nickname
}

func (pm *ProfileManager) SetNickname(name string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.Profile.Nickname = name
	pm.save()
}

func (pm *ProfileManager) load() {
	path := filepath.Join(pm.DataDir, "profile.json")
	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, &pm.Profile)
		fmt.Printf("👤 Loaded Profile: %s\n", pm.Profile.Nickname)
	}
}

func (pm *ProfileManager) save() {
	path := filepath.Join(pm.DataDir, "profile.json")
	data, _ := json.MarshalIndent(pm.Profile, "", "  ")
	os.WriteFile(path, data, 0600)
}