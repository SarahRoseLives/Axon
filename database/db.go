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
    DB, err = sql.Open("sqlite", dbPath)
    if err != nil {
        log.Fatalf("❌ Database Error: %v", err)
    }

    // Performance Tuning for concurrent Access
    DB.SetMaxOpenConns(1) // SQLite allows only 1 writer, keep it safe
    DB.SetMaxIdleConns(1)

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
            direction TEXT, -- 'sent' or 'received'
            content TEXT,
            status TEXT, -- 'pending', 'delivered', 'read'
            timestamp DATETIME,
            FOREIGN KEY(peer_id) REFERENCES peers(onion_address)
        );`,

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
    fmt.Println("🗄️  Database initialized (SQLite)")
}