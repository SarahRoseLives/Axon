AXON // Mesh Network

v0.9.37-alpha

AXON is a decentralized, serverless P2P mesh built on Tor Hidden Services. It creates a private, encrypted network between you and your trusted peers, designed for high-latency, unreliable network environments.

There are no central servers, no phone numbers, and no metadata leaks.

🛡️ Key Features

```
Tor-Native: Every node is a Hidden Service (v3 .onion). Connections are end-to-end encrypted, anonymous, and NAT-punching is automatic.

Swarm Downloading: Axon aggregates bandwidth from multiple peers. If 5 friends have a file, you download chunks from all 5 simultaneously, maximizing speed over Tor.

Smart Resume: Network drops are expected. Transfers utilize persistent state files (.axon_state), allowing downloads to resume instantly—even after a full system restart.

Bloom Filter Indexing: Instead of exchanging massive file lists, nodes exchange bandwidth-efficient probabilistic filters (64KB). This allows libraries of 100,000+ files to be searchable without clogging the mesh.

Trust Graph: A visual, force-directed topology map showing your direct neighbors and the peers they know.
```

🚀 Getting Started

1. Run the Node

Axon ships as a single static binary. It manages its own Tor daemon.


# Start the node (binds Web UI to localhost:8080)

./axon

# Run a second instance on the same machine for testing

./axon -port 8081

2. Access the Interface

Open your browser and navigate to: [http://127.0.0.1:8081](http://127.0.0.1:8081)

📖 User Guide

1. Identity Setup

   Go to the Identity tab.

   Set a Display Name (e.g., "Ghost").

   Copy your Onion Address (e.g., pnkt...tqd.onion). This is your permanent, sovereign ID.

2. Building Your Mesh

   Go to the Topology tab.

   Click the Add Peer (+) icon.

   Paste a friend's Onion Address.

   Note: The initial handshake takes 30–60 seconds while Tor builds the circuit. Once connected, they appear on your map.

3. File Sharing (The Library)

   Hosting: Any file placed in your ./data/shared folder is automatically indexed into your local Bloom Filter and made searchable to peers.

   Searching: Go to the Library tab. Type a keyword (e.g., "blueprints"). Axon queries your neighbors' filters in real-time.

   Downloading: Click Download. The Swarm Engine will automatically find other peers who possess the file and parallelize the transfer.

4. Security & Moderation

   SSRF Protection: Strict input sanitization prevents malicious peers from probing your local network or localhost ports.

   Block: Click the Ban icon in chat to sever the connection and ignore future handshakes.

📡 Technical Protocol

AXON uses a custom JSON-over-HTTP protocol routed exclusively through Tor SOCKS5 proxies.

```
Discovery: Nodes exchange Bloom Filters (64KB bitsets) to advertise content availability without leaking exact file metadata to passive observers.

Transport: * Identity: Ed25519 (Signing/Auth)

    Encryption: X25519 (Chat E2EE) + Tor Native Encryption

Storage: SQLite in WAL (Write-Ahead Logging) mode ensures high concurrency for chat logs and file indexing.

Persistence: Identity keys, state files, and databases are saved to the local ./data_[port]/ directory.
```

⚠️ Disclaimer

This is experimental software. While Axon uses industry-standard cryptographic primitives (Ed25519, SHA-256, AES-GCM), it has not been formally audited. Use at your own risk.
