import 'dart:convert';
import 'dart:io';
import 'package:tor_hidden_service/tor_hidden_service.dart';
import 'package:sqflite/sqflite.dart';
import 'package:path/path.dart';
import 'package:path_provider/path_provider.dart';
import 'package:shared_preferences/shared_preferences.dart'; // <--- ADDED
import 'database.dart';
import 'crypto_manager.dart';

class AxonClient {
  final TorHiddenService tor;
  final CryptoManager crypto;

  AxonClient(this.tor, this.crypto);

  // 1. Handshake
  Future<void> sendHandshake(String targetOnion) async {
    final myPub = await crypto.getChatPublicKey();
    final client = tor.getTorHttpClient();

    // --- FIX: Get Real Nickname ---
    final prefs = await SharedPreferences.getInstance();
    final myNick = prefs.getString('nickname') ?? 'Anonymous';
    final myOnion = (await tor.getOnionHostname())?.trim(); // Trim is crucial

    if (myOnion == null) {
      print("❌ Handshake aborted: My Onion Address is not ready.");
      return;
    }

    final payload = {
      'onion_address': myOnion,
      'public_key': myPub,
      'identity_key': '',
      'nickname': myNick, // <--- USING REAL NICKNAME
      'signature': ''
    };

    try {
      final uri = Uri.parse('http://$targetOnion/api/peers/announce');
      final request = await client.postUrl(uri);
      request.headers.contentType = ContentType.json;
      request.write(jsonEncode(payload));

      final response = await request.close();
      if (response.statusCode == 200) {
        final responseBody = await response.transform(utf8.decoder).join();
        final data = jsonDecode(responseBody);

        final db = await AxonDatabase.db;
        await db.insert('peers', {
          'onion_address': targetOnion,
          'nickname': data['nickname'],
          'public_key': data['public_key'],
          'trust_level': 'direct',
          'last_seen': DateTime.now().toIso8601String(),
        }, conflictAlgorithm: ConflictAlgorithm.replace);
        print("✅ Handshake successful with $targetOnion");
      }
    } catch (e) {
      print("❌ Handshake failed: $e");
    }
  }

  // 2. Send Message
  Future<void> sendMessage(String targetOnion, String content) async {
    final db = await AxonDatabase.db;
    final List<Map> maps = await db.query('peers', where: 'onion_address = ?', whereArgs: [targetOnion]);

    if (maps.isEmpty) {
      print("Unknown peer");
      return;
    }
    final peerKey = maps.first['public_key'];
    final encrypted = await crypto.encrypt(peerKey, content);

    // --- FIX: Ensure 'from' is valid ---
    final myOnion = (await tor.getOnionHostname())?.trim();
    if (myOnion == null) {
      print("❌ Send aborted: My Onion Address is not ready.");
      return;
    }

    final payload = {
      'id': DateTime.now().millisecondsSinceEpoch.toString(),
      'from': myOnion, // Must not be null
      'ciphertext': encrypted['ciphertext'],
      'nonce': encrypted['nonce'],
    };

    final client = tor.getTorHttpClient();
    try {
      final uri = Uri.parse('http://$targetOnion/api/chat/recv');
      final request = await client.postUrl(uri);
      request.headers.contentType = ContentType.json;
      request.write(jsonEncode(payload));

      final response = await request.close();
      if (response.statusCode == 200) {
        await db.insert('messages', {
          'id': payload['id'],
          'peer_id': targetOnion,
          'direction': 'out',
          'content': content,
          'status': 'sent',
          'timestamp': DateTime.now().toIso8601String(),
        });
        print("✅ Sent message to $targetOnion");
      } else {
        print("❌ Send failed: Server returned ${response.statusCode}");
      }
    } catch (e) {
      print("❌ Send failed: $e");
    }
  }

  // 3. Search Remote Files
  Future<List<Map<String, dynamic>>> searchRemoteFiles(String query) async {
    final db = await AxonDatabase.db;
    final client = tor.getTorHttpClient();
    List<Map<String, dynamic>> results = [];

    final peers = await db.query('peers');

    await Future.wait(peers.map((peer) async {
      try {
        final onion = peer['onion_address'];
        final uri = Uri.parse('http://$onion/api/file/search?q=$query');
        final request = await client.getUrl(uri);
        final response = await request.close();

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
    final client = tor.getTorHttpClient();
    final saveDir = await getApplicationDocumentsDirectory();

    final downloadsDir = Directory(join(saveDir.path, 'downloads'));
    if (!await downloadsDir.exists()) {
      await downloadsDir.create(recursive: true);
    }

    final savePath = join(downloadsDir.path, fileName);
    final file = File(savePath);
    final raf = await file.open(mode: FileMode.write);

    const chunkSize = 512 * 1024;
    final totalChunks = (size / chunkSize).ceil();

    print("📥 Starting download: $fileName");

    try {
      for (int i = 0; i < totalChunks; i++) {
        final uri = Uri.parse('http://$peerOnion/api/file/chunk?id=$fileId&idx=$i');
        final request = await client.getUrl(uri);
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