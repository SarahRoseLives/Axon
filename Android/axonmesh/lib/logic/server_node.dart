import 'dart:convert';
import 'package:shelf/shelf.dart';
import 'package:shelf/shelf_io.dart' as shelf_io;
import 'package:shelf_router/shelf_router.dart';
import 'package:sqflite/sqflite.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'database.dart';
import 'crypto_manager.dart';

class AxonServer {
  final CryptoManager crypto;

  // We don't need to know our own address to start the server,
  // but it's good to have for logs.

  AxonServer(this.crypto);

  Future<void> start() async {
    final app = Router();

    // Define Routes
    app.post('/api/peers/announce', _handleAnnounce);
    app.post('/api/chat/recv', _handleChatRecv);
    app.post('/api/file/manifest', _handleManifest);
    app.get('/api/file/search', _handleSearch); // Added search handler
    app.get('/api/file/chunk', _handleFileChunk);

    // Middleware pipeline (Logger + Headers)
    final handler = Pipeline()
        .addMiddleware(logRequests())
        .addMiddleware(_corsMiddleware) // Add CORS/Headers
        .addHandler(app);

    // Bind to ANY interface (0.0.0.0) or Loopback.
    // Since the Tor Plugin forwards to localhost, '127.0.0.1' is safer.
    await shelf_io.serve(handler, '127.0.0.1', 8080);
    print('🚀 Axon Server listening on 127.0.0.1:8080');
  }

  // --- MIDDLEWARE ---
  Handler _corsMiddleware(Handler handler) {
    return (Request request) async {
      final response = await handler(request);
      return response.change(headers: {
        'Access-Control-Allow-Origin': '*',
        'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
        'Access-Control-Allow-Headers': 'Content-Type',
      });
    };
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

    final prefs = await SharedPreferences.getInstance();
    final myNick = prefs.getString('nickname') ?? 'Anonymous';
    final myPub = await crypto.getChatPublicKey();

    return Response.ok(jsonEncode({
      'peers': [], // Logic to share peers could go here (Gossip)
      'public_key': myPub,
      'nickname': myNick
    }), headers: {'content-type': 'application/json'});
  }

  Future<Response> _handleManifest(Request request) async {
    // Placeholder for receiving file manifests (Bloom filters)
    return Response.ok('OK');
  }

  Future<Response> _handleChatRecv(Request request) async {
    final payload = await request.readAsString();
    final data = jsonDecode(payload);

    final from = data['from'];
    final cipher = data['ciphertext'];
    final nonce = data['nonce'];
    final msgId = data['id'];

    final db = await AxonDatabase.db;

    // Dedup check
    final existing = await db.query('messages', where: 'id = ?', whereArgs: [msgId]);
    if (existing.isNotEmpty) {
      return Response.ok('Already received');
    }

    // Verify Sender
    final List<Map> maps = await db.query('peers', where: 'onion_address = ?', whereArgs: [from]);
    if (maps.isEmpty) return Response.forbidden('Unknown Peer. Please Handshake first.');
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

  // Handle incoming search requests
  Future<Response> _handleSearch(Request request) async {
    final query = request.url.queryParameters['q'];
    if (query == null || query.isEmpty) return Response.ok('[]');

    final db = await AxonDatabase.db;
    // Simple LIKE search on local files
    final results = await db.query(
      'my_files',
      where: 'name LIKE ?',
      whereArgs: ['%$query%']
    );

    return Response.ok(jsonEncode(results), headers: {'content-type': 'application/json'});
  }

  Future<Response> _handleFileChunk(Request request) async {
    // TODO: Implement file reading from 'my_files' table paths
    // For now, return 404 to avoid crashing
    return Response.notFound('File serving not implemented yet');
  }
}