import 'package:flutter/material.dart';
import '../logic/database.dart';
import 'chat_detail_screen.dart'; // We will create this next

class ChatScreen extends StatefulWidget {
  const ChatScreen({super.key});

  @override
  State<ChatScreen> createState() => _ChatScreenState();
}

class _ChatScreenState extends State<ChatScreen> {
  List<Map<String, dynamic>> _threads = [];

  @override
  void initState() {
    super.initState();
    _loadThreads();
  }

  Future<void> _loadThreads() async {
    final db = await AxonDatabase.db;
    // Get distinct peers from messages
    final result = await db.rawQuery('''
      SELECT 
        m.peer_id,
        MAX(m.timestamp) as last_msg_time,
        (SELECT content FROM messages WHERE peer_id = m.peer_id ORDER BY timestamp DESC LIMIT 1) as snippet,
        (SELECT nickname FROM peers WHERE onion_address = m.peer_id) as nickname
      FROM messages m
      GROUP BY m.peer_id
      ORDER BY last_msg_time DESC
    ''');

    if (mounted) setState(() => _threads = result);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      floatingActionButton: FloatingActionButton(
        backgroundColor: const Color(0xFF06b6d4),
        child: const Icon(Icons.refresh),
        onPressed: _loadThreads,
      ),
      body: _threads.isEmpty
        ? const Center(child: Text("No conversations yet", style: TextStyle(color: Colors.grey)))
        : ListView.builder(
            itemCount: _threads.length,
            itemBuilder: (context, index) {
              final thread = _threads[index];
              final peerId = thread['peer_id'];
              final nick = thread['nickname'] ?? peerId.toString().substring(0, 16);

              return ListTile(
                onTap: () {
                  Navigator.push(
                    context,
                    MaterialPageRoute(builder: (_) => ChatDetailScreen(peerId: peerId, nickname: nick))
                  ).then((_) => _loadThreads());
                },
                leading: const CircleAvatar(child: Icon(Icons.person)),
                title: Text(nick, style: const TextStyle(color: Colors.white)),
                subtitle: Text(
                  thread['snippet'] ?? '',
                  style: const TextStyle(color: Colors.grey),
                  maxLines: 1, overflow: TextOverflow.ellipsis,
                ),
                trailing: Text(
                  _formatTime(thread['last_msg_time']),
                  style: const TextStyle(color: Colors.grey, fontSize: 10),
                ),
              );
            },
          ),
    );
  }

  String _formatTime(String iso) {
    final dt = DateTime.parse(iso).toLocal();
    return "${dt.hour.toString().padLeft(2,'0')}:${dt.minute.toString().padLeft(2,'0')}";
  }
}