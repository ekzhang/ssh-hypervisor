package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// UserStat represents connection statistics for a single user
type UserStat struct {
	Username      string    `json:"username"`
	ConnectCount  int       `json:"connect_count"`
	LastConnected time.Time `json:"last_connected"`
}

// UserStats manages user connection statistics
type UserStats struct {
	mu       sync.Mutex
	users    map[string]*UserStat
	dataFile string
}

// NewUserStats creates a new UserStats manager
func NewUserStats(dataDir string) *UserStats {
	return &UserStats{
		users:    make(map[string]*UserStat),
		dataFile: filepath.Join(dataDir, "user_stats.json"),
	}
}

// Load reads user statistics from the JSON file
func (us *UserStats) Load() error {
	us.mu.Lock()
	defer us.mu.Unlock()

	if _, err := os.Stat(us.dataFile); os.IsNotExist(err) {
		// File doesn't exist, start with empty stats
		return nil
	}

	data, err := os.ReadFile(us.dataFile)
	if err != nil {
		return err
	}

	var users []*UserStat
	if err := json.Unmarshal(data, &users); err != nil {
		return err
	}

	// Convert slice to map
	us.users = make(map[string]*UserStat)
	for _, user := range users {
		us.users[user.Username] = user
	}

	return nil
}

// Save writes user statistics to the JSON file
func (us *UserStats) Save() error {
	us.mu.Lock()
	defer us.mu.Unlock()

	// Convert map to slice for JSON serialization
	users := make([]*UserStat, 0, len(us.users))
	for _, user := range us.users {
		users = append(users, user)
	}

	// Sort by last connected time (most recent first)
	sort.Slice(users, func(i, j int) bool {
		return users[i].LastConnected.After(users[j].LastConnected)
	})

	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(us.dataFile), 0755); err != nil {
		return err
	}

	return os.WriteFile(us.dataFile, data, 0644)
}

// RecordConnection records a user connection
func (us *UserStats) RecordConnection(username string) {
	us.mu.Lock()
	defer us.mu.Unlock()

	if user, exists := us.users[username]; exists {
		user.ConnectCount++
		user.LastConnected = time.Now()
	} else {
		us.users[username] = &UserStat{
			Username:      username,
			ConnectCount:  1,
			LastConnected: time.Now(),
		}
	}
}

// GetUserStat returns statistics for a specific user
func (us *UserStats) GetUserStat(username string) (*UserStat, bool) {
	us.mu.Lock()
	defer us.mu.Unlock()

	user, exists := us.users[username]
	return user, exists
}

// GetRecentUsers returns the most recent users (excluding the current user)
func (us *UserStats) GetRecentUsers(excludeUser string, limit int) []*UserStat {
	us.mu.Lock()
	defer us.mu.Unlock()

	users := make([]*UserStat, 0, len(us.users))
	for _, user := range us.users {
		if user.Username != excludeUser {
			users = append(users, &UserStat{
				Username:      user.Username,
				ConnectCount:  user.ConnectCount,
				LastConnected: user.LastConnected,
			})
		}
	}

	// Sort by last connected time (most recent first)
	sort.Slice(users, func(i, j int) bool {
		return users[i].LastConnected.After(users[j].LastConnected)
	})

	if limit > 0 && len(users) > limit {
		users = users[:limit]
	}

	return users
}
