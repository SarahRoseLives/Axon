// ====screens/chat_detail_screen.dart====
import 'dart:async'; // Import for StreamSubscription
import 'package:flutter/material.dart';
import 'package:sqflite/sqflite.dart';
import '../logic/database.dart';
import '../logic/outbox.dart';
import '../logic/events.dart'; // <--- IMPORT EVENTS
import '../main.dart';

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
  StreamSubscription? _msgSubscription; // <--- Subscription

  @override
  void initState() {
    super.initState();
    _loadMessages();

    // --- LISTEN FOR REAL-TIME UPDATES ---
    _msgSubscription = AxonEvents.onMessage.listen((_) {
      print("🔄 UI: Received update event, reloading messages...");
      _loadMessages();
    });
  }

  @override
  void dispose() {
    _msgSubscription?.cancel(); // <--- Prevent Memory Leaks
    _controller.dispose();
    super.dispose();
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
      'status': 'sending',
      'timestamp': DateTime.now().toIso8601String(),
    });

    await _loadMessages(); // Refresh local view immediately

    // 2. Trigger Network Call
    AxonApp.client.sendMessage(widget.peerId, content).then((_) {
        // The Outbox will trigger AxonEvents.triggerMessageUpdate()
        // which will be caught by the listener above to update 'sending' -> 'sent'
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