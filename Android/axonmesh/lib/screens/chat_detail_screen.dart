import 'package:flutter/material.dart';
import 'package:sqflite/sqflite.dart';
import '../logic/database.dart';
import '../logic/outbox.dart'; // Import Outbox logic
import '../main.dart'; // Access AxonApp.client

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
    if (mounted) {
      setState(() => _messages = res);
    }
  }

  Future<void> _send() async {
    if (_controller.text.isEmpty) return;
    final content = _controller.text;
    _controller.clear();

    // 1. Optimistic UI Update (Save to DB as 'sending')
    final db = await AxonDatabase.db;
    await db.insert('messages', {
      'id': DateTime.now().millisecondsSinceEpoch.toString(),
      'peer_id': widget.peerId,
      'direction': 'out',
      'content': content,
      'status': 'sending', // Temporary status
      'timestamp': DateTime.now().toIso8601String(),
    });

    await _loadMessages();

    // 2. TRIGGER THE NETWORK CALL (This was missing!)
    print("UI: Triggering sendMessage to ${widget.peerId}");

    // We don't await this so the UI doesn't freeze if Tor is slow
    AxonApp.client.sendMessage(widget.peerId, content).then((_) {
      print("UI: Send returned");
      // Optional: Reload to update status from 'sending' to 'sent'
      _loadMessages();
    });
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
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.end,
                      children: [
                        Text(msg['content'] ?? '', style: const TextStyle(color: Colors.white)),
                        if (isMe)
                          Text(
                            msg['status'] ?? '',
                            style: TextStyle(fontSize: 10, color: Colors.white.withOpacity(0.5))
                          )
                      ],
                    ),
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
                    style: const TextStyle(color: Colors.white),
                    decoration: const InputDecoration(
                      hintText: "Type a message...",
                      hintStyle: TextStyle(color: Colors.grey),
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