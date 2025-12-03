import 'dart:convert';
import 'dart:io';
import 'package:shelf/shelf.dart';
import 'package:shelf/shelf_io.dart' as shelf_io;
import 'package:shelf_router/shelf_router.dart';
import 'package:sqflite/sqflite.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'database.dart';
import 'crypto_manager.dart';

class AxonServer {
  final CryptoManager crypto;

  AxonServer(this.crypto);

  Future<void> start() async {
    final app = Router();

    app.post('/api/peers/announce', _handleAnnounce);
    app.post('/api/chat/recv', _handleChatRecv);
    app.post('/api/file/manifest', _handleManifest);
    app.get('/api/file/search', _handleSearch);
    app.get('/api/file/chunk', _handleFileChunk);

    final handler = Pipeline()
        .addMiddleware(logRequests())
        .addMiddleware(_corsMiddleware)
        .addHandler(app);

    // ⚠️ TRIPLE CHECK: Must be InternetAddress.anyIPv4
    // Binding to 'localhost' is the #1 cause of 502 errors on Android Tor apps.
    await shelf_io.serve(
      handler,
      InternetAddress.anyIPv4,
      8080,
      shared: true
    );

    print('🚀 Axon Server listening on 0.0.0.0:8080');
  }

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

  // --- HANDLERS (Unchanged logic) ---

  Future<Response> _handleAnnounce(Request request) async {
    try {
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
        'peers': [],
        'public_key': myPub,
        'nickname': myNick
      }), headers: {'content-type': 'application/json'});
    } catch (e) {
      print("Error in announce: $e");
      return Response.internalServerError();
    }
  }

  Future<Response> _handleManifest(Request request) async => Response.ok('OK');

  Future<Response> _handleChatRecv(Request request) async {
    try {
      final payload = await request.readAsString();
      final data = jsonDecode(payload);

      final from = data['from'];
      final cipher = data['ciphertext'];
      final nonce = data['nonce'];
      final msgId = data['id'];

      final db = await AxonDatabase.db;

      final existing = await db.query('messages', where: 'id = ?', whereArgs: [msgId]);
      if (existing.isNotEmpty) return Response.ok('Already received');

      final List<Map> maps = await db.query('peers', where: 'onion_address = ?', whereArgs: [from]);
      if (maps.isEmpty) return Response.forbidden('Unknown Peer');
      final peerPubKey = maps.first['public_key'];

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

  Future<Response> _handleSearch(Request request) async {
    final query = request.url.queryParameters['q'];
    if (query == null || query.isEmpty) return Response.ok('[]');
    final db = await AxonDatabase.db;
    final results = await db.query('my_files', where: 'name LIKE ?', whereArgs: ['%$query%']);
    return Response.ok(jsonEncode(results), headers: {'content-type': 'application/json'});
  }

  Future<Response> _handleFileChunk(Request request) async => Response.notFound('Not implemented');
}