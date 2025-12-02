import 'package:flutter/material.dart';
import 'package:sqflite/sqflite.dart'; // Add import for ConflictAlgorithm if needed
import '../logic/database.dart';
import '../logic/outbox.dart';
import '../logic/server_node.dart'; // To access client? ideally client passes down
import '../main.dart'; // To access global state or use GetIt/Provider

class ChatDetailScreen extends StatefulWidget {
  final String peerId;
  final String nickname;

  const ChatDetailScreen({super.key, required this.peerId, required this.nickname});

  @override
  State<ChatDetailScreen> createState() => _ChatDetailScreenState();
}

class _ChatDetailScreenState extends State<ChatDetailScreen> {
  final TextEditingController _controller = TextEditingController();
  List<Map<String, dynamic>> _messages = [];

  // NOTE: In a real app, use Provider/Riverpod to get these services
  // For now, we assume you can create a temporary instance or pass it
  // We will just read from DB and 'pretend' to send for UI demo if services aren't passed
  // To make this work properly, passing the AxonClient via Constructor is best.

  @override
  void initState() {
    super.initState();
    _loadMessages();
  }

  Future<void> _loadMessages() async {
    final db = await AxonDatabase.db;
    final res = await db.query(
      'messages',
      where: 'peer_id = ?',
      whereArgs: [widget.peerId],
      orderBy: 'timestamp ASC'
    );
    setState(() => _messages = res);
  }

  Future<void> _send() async {
    if (_controller.text.isEmpty) return;
    final content = _controller.text;
    _controller.clear();

    // 1. Optimistic UI Update (Save to DB as 'pending' or 'out')
    final db = await AxonDatabase.db;
    await db.insert('messages', {
      'id': DateTime.now().millisecondsSinceEpoch.toString(),
      'peer_id': widget.peerId,
      'direction': 'out',
      'content': content,
      'status': 'sending',
      'timestamp': DateTime.now().toIso8601String(),
    });

    await _loadMessages();

    // 2. Actually Send (Requires access to AxonClient)
    // For this port, we need to expose AxonClient globally or pass it.
    // print("TODO: Trigger AxonClient.sendMessage('$content')");
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(widget.nickname)),
      body: Column(
        children: [
          Expanded(
            child: ListView.builder(
              padding: const EdgeInsets.all(10),
              itemCount: _messages.length,
              itemBuilder: (context, index) {
                final msg = _messages[index];
                final isMe = msg['direction'] == 'out';
                return Align(
                  alignment: isMe ? Alignment.centerRight : Alignment.centerLeft,
                  child: Container(
                    margin: const EdgeInsets.symmetric(vertical: 4),
                    padding: const EdgeInsets.all(10),
                    decoration: BoxDecoration(
                      color: isMe ? const Color(0xFF06b6d4) : const Color(0xFF374151),
                      borderRadius: BorderRadius.circular(8),
                    ),
                    child: Text(msg['content'] ?? '', style: const TextStyle(color: Colors.white)),
                  ),
                );
              },
            ),
          ),
          Padding(
            padding: const EdgeInsets.all(8.0),
            child: Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: _controller,
                    decoration: const InputDecoration(
                      hintText: "Type a message...",
                      border: OutlineInputBorder(),
                      filled: true,
                      fillColor: Color(0xFF1f2937),
                    ),
                  ),
                ),
                IconButton(
                  icon: const Icon(Icons.send, color: Color(0xFF06b6d4)),
                  onPressed: _send,
                )
              ],
            ),
          )
        ],
      ),
    );
  }
}