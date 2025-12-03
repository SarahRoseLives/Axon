import 'dart:convert';
import 'dart:io';
import 'package:tor_hidden_service/tor_hidden_service.dart';
import 'package:path_provider/path_provider.dart';
import 'package:sqflite/sqflite.dart';
import 'package:path/path.dart' as p;
import 'package:shared_preferences/shared_preferences.dart';
import 'database.dart';
import 'crypto_manager.dart';
import 'events.dart';
import 'file_manager.dart';

class AxonClient {
  final TorHiddenService tor;
  final CryptoManager crypto;
  late final TorOnionClient _onionClient; // Uses the updated Plugin Client
  final FileManager fileMgr = FileManager();

  AxonClient(this.tor, this.crypto) {
    // This now returns the ROBUST client from your updated plugin
    _onionClient = tor.getUnsecureTorClient();
    fileMgr.init();
  }

  String _prepareUrl(String onion, String path) {
    var host = onion.trim();
    host = host.replaceFirst(RegExp(r'^https?://'), '');
    if (host.endsWith('/')) host = host.substring(0, host.length - 1);
    if (!path.startsWith('/')) path = '/$path';
    return 'http://$host$path';
  }

  Future<void> sendHandshake(String targetOnion) async {
    final myPub = await crypto.getChatPublicKey();
    final prefs = await SharedPreferences.getInstance();
    final myNick = prefs.getString('nickname') ?? 'Anonymous';
    final myOnion = (await tor.getOnionHostname())?.trim();

    if (myOnion == null) return;

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
      final response = await _onionClient.post(
        url,
        body: jsonEncode(payload),
        headers: {'Content-Type': 'application/json'}
      );

      print("📥 [HANDSHAKE] Status: ${response.statusCode}");

      if (response.statusCode == 200) {
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

  Future<void> sendMessage(String targetOnion, String content) async {
    final cleanTarget = targetOnion.trim();
    final db = await AxonDatabase.db;
    final List<Map> maps = await db.query('peers', where: 'onion_address = ?', whereArgs: [cleanTarget]);

    if (maps.isEmpty) return;
    final peerKey = maps.first['public_key'];

    Map<String, String> encrypted;
    try {
      encrypted = await crypto.encrypt(peerKey, content);
    } catch (e) { return; }

    final myOnion = (await tor.getOnionHostname())?.trim();
    if (myOnion == null) return;

    final payload = {
      'id': DateTime.now().millisecondsSinceEpoch.toString(),
      'from': myOnion,
      'ciphertext': encrypted['ciphertext'],
      'nonce': encrypted['nonce'],
    };

    final url = _prepareUrl(cleanTarget, '/api/chat/recv');

    try {
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
        AxonEvents.triggerMessageUpdate();
        print("✅ [CHAT] Delivered.");
      }
    } catch (e) {
      print("❌ [CHAT] Network Exception: $e");
    }
  }

  // --- SEARCH FILES ---
  Future<List<Map<String, dynamic>>> searchRemoteFiles(String query) async {
    final candidates = fileMgr.searchFilters(query);
    print("🔍 [Search] Bloom Filter found ${candidates.length} candidates for '$query'");

    List<Map<String, dynamic>> results = [];

    await Future.wait(candidates.map((onion) async {
      final encodedQuery = Uri.encodeComponent(query);
      final url = _prepareUrl(onion, '/api/file/query?q=$encodedQuery');

      print("🚀 [Search] Requesting: $url");

      try {
        final response = await _onionClient.get(url);
        print("📥 [Search] Status: ${response.statusCode}");

        if (response.statusCode == 200) {
          try {
            // Because the plugin now buffers effectively, response.body is complete.
            final List<dynamic> files = jsonDecode(response.body);
            print("📦 [Search] Peer $onion returned ${files.length} matches.");

            for (var f in files) {
              results.add({
                'id': f['id'],
                'name': f['name'],
                'size': f['size'],
                'owner': onion,
              });
            }
          } catch (jsonErr) {
             print("❌ [Search] JSON Parse Error: $jsonErr");
          }
        } else {
           print("❌ [Search] HTTP Error ${response.statusCode}");
        }
      } catch (e) {
        print("⚠️ [Search] Connection Failed to $onion: $e");
      }
    }));

    print("✅ [Search] Aggregated ${results.length} total results.");
    return results;
  }

  Future<void> downloadFile(String peerOnion, String fileId, String fileName, int size) async {
    final saveDir = await getApplicationDocumentsDirectory();
    final downloadsDir = Directory(p.join(saveDir.path, 'downloads'));
    if (!await downloadsDir.exists()) await downloadsDir.create(recursive: true);

    final savePath = p.join(downloadsDir.path, fileName);
    final file = File(savePath);
    final raf = await file.open(mode: FileMode.write);

    const chunkSize = 512 * 1024;
    final totalChunks = (size / chunkSize).ceil();

    fileMgr.startDownload(fileId, fileName, size, peerOnion, (id, idx, total, data) async {});

    print("📥 Starting download: $fileName");

    // For Binary Downloads, we still use the Secure client (Standard HttpClient)
    // because TorOnionClient is optimized for UTF-8 Text/JSON.
    final client = tor.getSecureTorClient();

    try {
      for (int i = 0; i < totalChunks; i++) {
        var cleanOnion = peerOnion.replaceFirst(RegExp(r'^https?://'), '');
        if (cleanOnion.endsWith('/')) cleanOnion = cleanOnion.substring(0, cleanOnion.length - 1);

        final targetUrl = 'https://$cleanOnion/api/file/chunk?id=$fileId&idx=$i';
        final request = await client.getUrl(Uri.parse(targetUrl));
        final response = await request.close();

        if (response.statusCode == 200) {
          await for (final chunk in response) {
            await raf.writeFrom(chunk);
          }
          fileMgr.updateProgress(fileId, i);
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