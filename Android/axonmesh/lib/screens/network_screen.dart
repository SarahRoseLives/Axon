import 'dart:async';
import 'package:flutter/material.dart';
import '../logic/database.dart';
import 'chat_detail_screen.dart'; // <--- IMPORT THIS

class NetworkScreen extends StatefulWidget {
  const NetworkScreen({super.key});

  @override
  State<NetworkScreen> createState() => _NetworkScreenState();
}

class _NetworkScreenState extends State<NetworkScreen> {
  List<Map<String, dynamic>> _peers = [];
  Timer? _timer;

  @override
  void initState() {
    super.initState();
    _refreshPeers();
    // Poll every 2 seconds for new peers
    _timer = Timer.periodic(const Duration(seconds: 2), (_) => _refreshPeers());
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _refreshPeers() async {
    final db = await AxonDatabase.db;
    final data = await db.query('peers', orderBy: 'last_seen DESC');
    if (mounted) {
      setState(() => _peers = data);
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_peers.isEmpty) {
      return const Center(
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Icon(Icons.hub, size: 64, color: Colors.grey),
            SizedBox(height: 16),
            Text("No peers discovered yet.", style: TextStyle(color: Colors.grey)),
            Text("Click + to add a bootstrap node.", style: TextStyle(color: Colors.grey)),
          ],
        ),
      );
    }

    return ListView.builder(
      itemCount: _peers.length,
      itemBuilder: (context, index) {
        final peer = _peers[index];
        final lastSeen = DateTime.parse(peer['last_seen']);
        final isOnline = DateTime.now().difference(lastSeen).inMinutes < 5;
        final nickname = peer['nickname'] ?? 'Anonymous';
        final onion = peer['onion_address'];

        return Card(
          margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
          color: const Color(0xFF1f2937),
          child: ListTile(
            // --- FIX: OPEN CHAT ON TAP ---
            onTap: () {
              Navigator.push(
                context,
                MaterialPageRoute(
                  builder: (_) => ChatDetailScreen(
                    peerId: onion,
                    nickname: nickname
                  )
                )
              );
            },
            // -----------------------------
            leading: CircleAvatar(
              backgroundColor: isOnline ? Colors.green : Colors.grey,
              radius: 6,
            ),
            title: Text(
              nickname,
              style: const TextStyle(color: Colors.white, fontWeight: FontWeight.bold),
            ),
            subtitle: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  onion,
                  style: const TextStyle(color: Colors.grey, fontFamily: 'Courier', fontSize: 10),
                  overflow: TextOverflow.ellipsis,
                ),
                Text(
                  "Trust: ${peer['trust_level']} • Seen: ${_timeAgo(lastSeen)}",
                  style: const TextStyle(color: Color(0xFF06b6d4), fontSize: 10),
                ),
              ],
            ),
            trailing: const Icon(Icons.chevron_right, color: Colors.grey),
          ),
        );
      },
    );
  }

  String _timeAgo(DateTime d) {
    final diff = DateTime.now().difference(d);
    if (diff.inSeconds < 60) return "${diff.inSeconds}s ago";
    if (diff.inMinutes < 60) return "${diff.inMinutes}m ago";
    return "${diff.inHours}h ago";
  }
}