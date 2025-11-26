AXON // Mesh Network

AXON is a decentralized, serverless messenger built on Tor Onion Services. It creates a private, encrypted mesh network between you and your trusted peers. There are no central servers, no phone numbers, and no metadata leaks.

🛡️ Key Features

    Tor-Native: Every node is a Hidden Service (.onion). Connections are anonymous and NAT-punching is automatic.

    Trust Graph: A visual topology map showing your direct neighbors and the peers they know (transitive trust).

    End-to-End Encrypted: All messages are secured using X25519 key exchange and AES-256-GCM.

    Store & Forward: Messages sent to offline peers are queued locally and auto-delivered when they return online.

    Gossip Discovery: Your node automatically learns about "friends of friends," expanding your network organically.

🚀 Getting Started

1. Run the Node

Start the application. It will automatically launch a local Tor instance and bind the Web UI to port 8080.
Bash

./axon

To run multiple instances on the same machine (for testing), use the port flag:
Bash

./axon -port 8080

2. Access the Interface

Open your browser and navigate to: http://127.0.0.1:8080

📖 User Guide

1. Identity Setup

    Go to the Identity tab.

    Set a Display Name (e.g., "Ghost"). This is how you will appear to others.

    Copy your Onion Address (e.g., pnkt...tqd.onion). This is your permanent ID.

2. Adding a Peer

    Go to the Topology tab.

    Click the User Plus (+) icon in the top right.

    Paste your friend's Onion Address.

    Wait. The initial handshake takes 30-60 seconds while Tor builds a circuit.

    Once connected, their node will appear as a bubble on your map.

3. Chatting

    Click any node on the Topology Map or select them from the Comms Sidebar.

    The connection status (Online/Offline) is handled automatically.

    ✓ Green check: Message delivered.

    🕒 Grey clock: Message pending (peer is offline; will retry automatically).

4. Managing Peers

    Delete: In the chat window, click the Trash icon to remove a peer and wipe history.

    Block: Click the Ban icon to ignore all future connection attempts from that address.

📡 Technical Protocol

AXON uses a custom JSON-over-HTTP protocol routed exclusively through Tor SOCKS5 proxies.

    Handshake: Nodes exchange persistent Identity Keys (Ed25519) and ephemeral Encryption Keys (X25519).

    Gossip: Every 60 seconds, nodes exchange partial peer lists with random neighbors to maintain the mesh.

    Persistence: Identity keys, peer lists, and chat history are saved to the local ./data_[port]/ directory.

Disclaimer: This is experimental software. While it uses industry-standard crypto primitives, it has not been audited.