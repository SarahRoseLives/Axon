package database

import (
    "database/sql"
    "fmt"
    "log"

    _ "modernc.org/sqlite" // Pure Go SQLite driver
)

var DB *sql.DB

func Init(dataDir string) {
    dbPath := fmt.Sprintf("%s/axon.db", dataDir)
    var err error

    // Open DB
    // We add connection string parameters to optimize locking behavior
    DB, err = sql.Open("sqlite", dbPath)
    if err != nil {
        log.Fatalf("❌ Database Error: %v", err)
    }

    // --- PERFORMANCE TUNING (The Fix) ---

    // 1. Enable Write-Ahead Logging (WAL)
    // This allows readers (UI) and writers (Tor) to coexist without blocking.
    if _, err := DB.Exec("PRAGMA journal_mode=WAL;"); err != nil {
        fmt.Printf("⚠️ Failed to enable WAL mode: %v\n", err)
    }

    // 2. Set Busy Timeout
    // Prevents "database is locked" errors by waiting up to 5s for the lock
    if _, err := DB.Exec("PRAGMA busy_timeout=5000;"); err != nil {
        fmt.Printf("⚠️ Failed to set busy timeout: %v\n", err)
    }

    // 3. Synchronous NORMAL
    // Faster writes, slightly less safe against OS crashes (but fine for chat app)
    if _, err := DB.Exec("PRAGMA synchronous=NORMAL;"); err != nil {
         fmt.Printf("⚠️ Failed to set synchronous mode: %v\n", err)
    }

    // 4. Increase Connection Pool
    // WAL mode supports concurrency. 1 is too restrictive.
    DB.SetMaxOpenConns(25)
    DB.SetMaxIdleConns(25)
    // ------------------------------------

    createTables()
}

func createTables() {
    queries := []string{
        // PEERS TABLE
        `CREATE TABLE IF NOT EXISTS peers (
            onion_address TEXT PRIMARY KEY,
            nickname TEXT,
            public_key TEXT,
            trust_level TEXT,
            introduced_by TEXT,
            last_seen DATETIME,
            is_blocked BOOLEAN DEFAULT 0
        );`,

        // MESSAGES TABLE
        `CREATE TABLE IF NOT EXISTS messages (
            id TEXT PRIMARY KEY,
            peer_id TEXT,
            direction TEXT, -- 'in', 'out', 'feed'
            content TEXT,
            status TEXT, -- 'pending', 'sent', 'received', 'read'
            timestamp DATETIME,
            FOREIGN KEY(peer_id) REFERENCES peers(onion_address)
        );`,
        // Index for faster chat history loading
        `CREATE INDEX IF NOT EXISTS idx_msg_peer_time ON messages(peer_id, timestamp);`,

        // FILES TABLE (Your Library)
        `CREATE TABLE IF NOT EXISTS my_files (
            id TEXT PRIMARY KEY,
            name TEXT,
            size INTEGER,
            path TEXT,
            hash TEXT
        );`,

        // REMOTE FILES (Search Index)
        `CREATE TABLE IF NOT EXISTS remote_files (
            id TEXT,
            name TEXT,
            size INTEGER,
            owner_id TEXT,
            last_updated DATETIME,
            PRIMARY KEY (id, owner_id)
        );`,
    }

    for _, q := range queries {
        _, err := DB.Exec(q)
        if err != nil {
            log.Fatalf("❌ Failed to create table: %v\nQuery: %s", err, q)
        }
    }
    fmt.Println("🗄️  Database initialized (SQLite WAL Mode)")
}