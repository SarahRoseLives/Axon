import 'dart:convert';
import 'dart:io';
import 'package:tor_hidden_service/tor_hidden_service.dart';
import 'package:path_provider/path_provider.dart';
import 'package:sqflite/sqflite.dart';
import 'package:path/path.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'database.dart';
import 'crypto_manager.dart';

class AxonClient {
  final TorHiddenService tor;
  final CryptoManager crypto;
  late final TorOnionClient _onionClient;

  AxonClient(this.tor, this.crypto) {
    // 🌟 Initialize the new client that handles the proxy tunnel automatically
    _onionClient = tor.getUnsecureTorClient();
  }

  // --- HELPER: Prepare HTTP URL ---
  String _prepareUrl(String onion, String path) {
    var host = onion.trim();
    // Remove existing schemes to prevent double protocol
    host = host.replaceFirst('http://', '').replaceFirst('https://', '');
    // Remove trailing slashes
    if (host.endsWith('/')) host = host.substring(0, host.length - 1);

    // 🌟 Use plain HTTP. The TorOnionClient handles the secure tunnel.
    return 'http://$host$path';
  }

  // 1. Handshake
  Future<void> sendHandshake(String targetOnion) async {
    final myPub = await crypto.getChatPublicKey();
    final prefs = await SharedPreferences.getInstance();
    final myNick = prefs.getString('nickname') ?? 'Anonymous';
    final myOnion = (await tor.getOnionHostname())?.trim();

    if (myOnion == null) {
      print("❌ [CLIENT] Handshake aborted: My Onion Address not ready.");
      return;
    }

    final payload = {
      'onion_address': myOnion,
      'public_key': myPub,
      'identity_key': '',
      'nickname': myNick,
      'signature': ''
    };

    final url = _prepareUrl(targetOnion, '/api/peers/announce');
    print("🧅 [HANDSHAKE] Sending to $url");

    try {
      // 🌟 USE NEW CLIENT
      final response = await _onionClient.post(
        url,
        body: jsonEncode(payload),
        headers: {'Content-Type': 'application/json'}
      );

      print("📥 [HANDSHAKE] Status: ${response.statusCode}");

      if (response.statusCode == 200) {
        // Response body is already a String! No transform needed.
        final data = jsonDecode(response.body);

        final db = await AxonDatabase.db;
        await db.insert('peers', {
          'onion_address': targetOnion.trim(),
          'nickname': data['nickname'],
          'public_key': data['public_key'],
          'trust_level': 'direct',
          'last_seen': DateTime.now().toIso8601String(),
        }, conflictAlgorithm: ConflictAlgorithm.replace);

        print("✅ [HANDSHAKE] Success!");
      }
    } catch (e) {
      print("❌ [HANDSHAKE] Error: $e");
    }
  }

  // 2. Send Message
  Future<void> sendMessage(String targetOnion, String content) async {
    final cleanTarget = targetOnion.trim();
    final db = await AxonDatabase.db;
    final List<Map> maps = await db.query('peers', where: 'onion_address = ?', whereArgs: [cleanTarget]);

    if (maps.isEmpty) {
      print("❌ [CLIENT] Abort: Unknown peer. Handshake first.");
      return;
    }
    final peerKey = maps.first['public_key'];

    Map<String, String> encrypted;
    try {
      encrypted = await crypto.encrypt(peerKey, content);
    } catch (e) {
      print("❌ [CLIENT] Encryption failed: $e");
      return;
    }

    final myOnion = (await tor.getOnionHostname())?.trim();
    if (myOnion == null) return;

    final payload = {
      'id': DateTime.now().millisecondsSinceEpoch.toString(),
      'from': myOnion,
      'ciphertext': encrypted['ciphertext'],
      'nonce': encrypted['nonce'],
    };

    final url = _prepareUrl(cleanTarget, '/api/chat/recv');
    print("🧅 [CHAT] Sending to $url");

    try {
      // 🌟 USE NEW CLIENT
      final response = await _onionClient.post(
        url,
        body: jsonEncode(payload),
        headers: {'Content-Type': 'application/json'}
      );

      if (response.statusCode == 200) {
        await db.insert('messages', {
          'id': payload['id'],
          'peer_id': cleanTarget,
          'direction': 'out',
          'content': content,
          'status': 'sent',
          'timestamp': DateTime.now().toIso8601String(),
        });
        print("✅ [CHAT] Delivered.");
      } else {
        print("⚠️ [CHAT] Rejected: ${response.body}");
      }
    } catch (e) {
      print("❌ [CHAT] Network Exception: $e");
    }
  }

  // 3. Search Remote Files
  Future<List<Map<String, dynamic>>> searchRemoteFiles(String query) async {
    final db = await AxonDatabase.db;
    List<Map<String, dynamic>> results = [];
    final peers = await db.query('peers');

    await Future.wait(peers.map((peer) async {
      try {
        final onion = peer['onion_address'];
        final url = _prepareUrl(onion.toString(), '/api/file/search?q=$query');

        // 🌟 USE NEW CLIENT
        final response = await _onionClient.get(url);

        if (response.statusCode == 200) {
          final List<dynamic> files = jsonDecode(response.body);
          for (var f in files) {
            results.add({
              'id': f['id'],
              'name': f['name'],
              'size': f['size'],
              'owner': onion,
            });
          }
        }
      } catch (e) {
        print("Search failed for ${peer['onion_address']}: $e");
      }
    }));
    return results;
  }

  // 4. Download File
  // ⚠️ NOTE: We CANNOT use TorOnionClient for binary downloads yet because
  // it forces UTF-8 decoding on the response.
  // We must fall back to the "Secure" client (HTTP Client wrapper) which supports byte streams.
  Future<void> downloadFile(String peerOnion, String fileId, String fileName, int size) async {
    final saveDir = await getApplicationDocumentsDirectory();
    final downloadsDir = Directory(join(saveDir.path, 'downloads'));
    if (!await downloadsDir.exists()) await downloadsDir.create(recursive: true);

    final savePath = join(downloadsDir.path, fileName);
    final file = File(savePath);
    final raf = await file.open(mode: FileMode.write);

    const chunkSize = 512 * 1024;
    final totalChunks = (size / chunkSize).ceil();

    print("📥 Starting download: $fileName");

    // 🌟 Use Secure Client for Binary Streams (requires https:// hack)
    final client = tor.getSecureTorClient();

    try {
      for (int i = 0; i < totalChunks; i++) {
        // Strip http/https to be safe, then force https://
        var cleanOnion = peerOnion.replaceFirst('http://', '').replaceFirst('https://', '');
        if (cleanOnion.endsWith('/')) cleanOnion = cleanOnion.substring(0, cleanOnion.length - 1);

        // Force HTTPS to trigger CONNECT tunnel for the standard HttpClient
        final targetUrl = 'https://$cleanOnion/api/file/chunk?id=$fileId&idx=$i';

        final request = await client.getUrl(Uri.parse(targetUrl));
        final response = await request.close();

        if (response.statusCode == 200) {
          await for (final chunk in response) {
            await raf.writeFrom(chunk);
          }
          print("✅ Chunk $i/$totalChunks received");
        } else {
          throw Exception("Chunk $i failed");
        }
      }
      print("🎉 Download Complete: $savePath");
    } catch (e) {
      print("❌ Download failed: $e");
    } finally {
      await raf.close();
    }
  }
}