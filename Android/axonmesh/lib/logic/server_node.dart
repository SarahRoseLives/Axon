import 'dart:convert';
import 'package:shelf/shelf.dart';
import 'package:shelf/shelf_io.dart' as shelf_io;
import 'package:shelf_router/shelf_router.dart';
import 'package:sqflite/sqflite.dart';
import 'package:shared_preferences/shared_preferences.dart'; // <--- ADDED
import 'database.dart';
import 'crypto_manager.dart';

class AxonServer {
  final CryptoManager crypto;
  String? myOnionAddress;

  AxonServer(this.crypto);

  Future<void> start() async {
    final app = Router();

    // 1. Handshake Endpoint
    app.post('/api/peers/announce', _handleAnnounce);

    // 2. Chat Endpoint
    app.post('/api/chat/recv', _handleChatRecv);

    // 3. File Endpoints
    app.get('/api/file/chunk', _handleFileChunk);

    final handler = Pipeline().addMiddleware(logRequests()).addHandler(app);

    await shelf_io.serve(handler, '127.0.0.1', 8080);
    print('🚀 Axon Server listening on localhost:8080');
  }

  // --- HANDLERS ---

  Future<Response> _handleAnnounce(Request request) async {
    final payload = await request.readAsString();
    final data = jsonDecode(payload);

    final from = data['onion_address'];
    final pubKey = data['public_key'];
    final nick = data['nickname'];

    print('👋 Handshake received from $from ($nick)');

    final db = await AxonDatabase.db;
    await db.insert('peers', {
      'onion_address': from,
      'nickname': nick,
      'public_key': pubKey,
      'trust_level': 'direct',
      'last_seen': DateTime.now().toIso8601String(),
    }, conflictAlgorithm: ConflictAlgorithm.replace);

    // --- FIX: Reply with Real Nickname ---
    final prefs = await SharedPreferences.getInstance();
    final myNick = prefs.getString('nickname') ?? 'Anonymous';
    final myPub = await crypto.getChatPublicKey();

    return Response.ok(jsonEncode({
      'peers': [],
      'public_key': myPub,
      'nickname': myNick // <--- USING REAL NICKNAME
    }));
  }

  Future<Response> _handleChatRecv(Request request) async {
    final payload = await request.readAsString();
    final data = jsonDecode(payload);

    final from = data['from'];
    final cipher = data['ciphertext'];
    final nonce = data['nonce'];
    final msgId = data['id'];

    final db = await AxonDatabase.db;

    // --- Deduplication Check ---
    final existing = await db.query('messages', where: 'id = ?', whereArgs: [msgId]);
    if (existing.isNotEmpty) {
      return Response.ok('Already received');
    }

    final List<Map> maps = await db.query('peers', where: 'onion_address = ?', whereArgs: [from]);
    if (maps.isEmpty) return Response.forbidden('Unknown Peer');
    final peerPubKey = maps.first['public_key'];

    try {
      final plaintext = await crypto.decrypt(peerPubKey, cipher, nonce);
      print('📨 Message from $from: $plaintext');

      await db.insert('messages', {
        'id': msgId,
        'peer_id': from,
        'direction': 'in',
        'content': plaintext,
        'status': 'received',
        'timestamp': DateTime.now().toIso8601String(),
      });

      return Response.ok('OK');
    } catch (e) {
      print('❌ Decryption failed: $e');
      return Response.internalServerError(body: 'Decryption Error');
    }
  }

  Future<Response> _handleFileChunk(Request request) async {
    return Response.ok('TODO: Chunk Data');
  }
}