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

  AxonClient(this.tor, this.crypto);

  // --- CORE NETWORK HELPER ---
  Future<HttpClientResponse> _sendOverTor(String method, String onionUrl, {Object? body}) async {
    // 1. FORCE HTTPS
    // This makes Dart HttpClient send a CONNECT command.
    // The Android Tor Plugin's HTTPTunnelPort (9080) requires CONNECT.
    // The Go Peer will receive this on port 443 and complete the TLS handshake.
    if (onionUrl.startsWith('http://')) {
      onionUrl = onionUrl.replaceFirst('http://', 'https://');
    } else if (!onionUrl.startsWith('https://')) {
      onionUrl = 'https://$onionUrl';
    }

    print("🧅 [CLIENT] Request: $method $onionUrl");

    // 2. Get the proxy-enabled client
    final client = tor.getTorHttpClient();

    try {
      final uri = Uri.parse(onionUrl);
      final request = await client.openUrl(method, uri);

      request.headers.contentType = ContentType.json;
      // Optional: Set host header for v3 onion validity
      request.headers.set(HttpHeaders.hostHeader, uri.host);

      if (body != null) {
        request.write(jsonEncode(body));
      }

      return await request.close();
    } catch (e) {
      print("❌ [CLIENT] Network Error to $onionUrl: $e");
      throw Exception("Tor Connection Failed");
    }
  }

  // 1. Handshake
  Future<void> sendHandshake(String targetOnion) async {
    final cleanTarget = targetOnion.trim();
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

    try {
      final targetUrl = 'http://$cleanTarget/api/peers/announce';
      final response = await _sendOverTor('POST', targetUrl, body: payload);

      print("📥 [CLIENT] Handshake Response: ${response.statusCode}");

      if (response.statusCode == 200) {
        final responseBody = await response.transform(utf8.decoder).join();
        final data = jsonDecode(responseBody);

        final db = await AxonDatabase.db;
        await db.insert('peers', {
          'onion_address': cleanTarget,
          'nickname': data['nickname'],
          'public_key': data['public_key'],
          'trust_level': 'direct',
          'last_seen': DateTime.now().toIso8601String(),
        }, conflictAlgorithm: ConflictAlgorithm.replace);
        print("✅ [CLIENT] Handshake successful!");
      }
    } catch (e) {
      print("❌ [CLIENT] Handshake Exception: $e");
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

    try {
      final targetUrl = 'http://$cleanTarget/api/chat/recv';
      final response = await _sendOverTor('POST', targetUrl, body: payload);

      print("📥 [CLIENT] Send Response: ${response.statusCode}");

      if (response.statusCode == 200) {
        await db.insert('messages', {
          'id': payload['id'],
          'peer_id': cleanTarget,
          'direction': 'out',
          'content': content,
          'status': 'sent',
          'timestamp': DateTime.now().toIso8601String(),
        });
        print("✅ [CLIENT] Message Delivered.");
      } else {
        final err = await response.transform(utf8.decoder).join();
        print("⚠️ [CLIENT] Server Rejected: $err");
      }
    } catch (e) {
      print("❌ [CLIENT] Network Exception: $e");
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
        final targetUrl = 'http://$onion/api/file/search?q=$query';

        final response = await _sendOverTor('GET', targetUrl);

        if (response.statusCode == 200) {
          final body = await response.transform(utf8.decoder).join();
          final List<dynamic> files = jsonDecode(body);
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
        print("Search failed for ${peer['onion_address']}");
      }
    }));
    return results;
  }

  // 4. Download File
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

    final client = tor.getTorHttpClient();

    try {
      for (int i = 0; i < totalChunks; i++) {
        final targetUrl = 'http://$peerOnion/api/file/chunk?id=$fileId&idx=$i';

        // Manual HTTPS enforcement here too
        final secureUrl = targetUrl.replaceFirst('http://', 'https://');

        final request = await client.getUrl(Uri.parse(secureUrl));
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