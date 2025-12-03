import 'dart:convert';
import 'dart:io';
import 'package:path_provider/path_provider.dart';
import 'package:path/path.dart' as p;
import 'package:sqflite/sqflite.dart';
import 'bloom_filter.dart';
import 'database.dart';

class TransferStatus {
  final String fileId;
  final String name;
  final String peerId;
  final double progress;
  final String status; // 'active', 'completed', 'failed'

  TransferStatus({required this.fileId, required this.name, required this.peerId, required this.progress, required this.status});
}

class FileManager {
  // Singleton
  static final FileManager _instance = FileManager._internal();
  factory FileManager() => _instance;
  FileManager._internal();

  BloomFilter localFilter = BloomFilter();
  final Map<String, BloomFilter> _remoteFilters = {};

  // Downloads: Map<FileID, DownloadState>
  final Map<String, _DownloadState> _activeDownloads = {};

  Future<void> init() async {
    await _scanSharedFolder();
  }

  // --- LOCAL LIBRARY ---

  Future<void> _scanSharedFolder() async {
    final dir = await getApplicationDocumentsDirectory();
    final sharedDir = Directory(p.join(dir.path, 'shared'));

    print("📂 [Library] Scanning directory: ${sharedDir.path}");

    if (!await sharedDir.exists()) {
      print("⚠️ [Library] Shared folder did not exist. Creating it.");
      await sharedDir.create();
    }

    final newFilter = BloomFilter();
    final db = await AxonDatabase.db;

    // Optional: Clear old entries to ensure the DB reflects reality
    await db.delete('my_files');

    if (await sharedDir.exists()) {
      try {
        await for (var entity in sharedDir.list()) {
          if (entity is File) {
            final name = p.basename(entity.path);

            // Skip hidden system files (like .DS_Store)
            if (name.startsWith('.')) continue;

            final size = await entity.length();
            // Simple ID generation
            final id = base64Encode(utf8.encode("$name-$size")).substring(0, 16);

            newFilter.add(name);

            print("   + Indexing File: $name ($size bytes) -> ID: $id");

            await db.insert('my_files', {
              'id': id,
              'name': name,
              'size': size,
              'path': entity.path,
              'hash': '',
            }, conflictAlgorithm: ConflictAlgorithm.replace);
          }
        }
      } catch (e) {
        print("❌ [Library] Scan Error: $e");
      }
    }

    localFilter = newFilter;

    // Verify DB count
    final count = Sqflite.firstIntValue(await db.rawQuery('SELECT COUNT(*) FROM my_files'));
    print("✅ [Library] Scan Complete. DB contains $count files.");
  }

  BloomFilter getLocalManifest() => localFilter;

  // --- REMOTE INDEX ---

  void processRemoteFilter(String peerId, BloomFilter filter) {
    _remoteFilters[peerId] = filter;
    print("🧠 Learned file index from $peerId");
  }

  List<String> searchFilters(String query) {
    final candidates = <String>[];

    // 1. Split query into keywords
    final tokens = BloomFilter.tokenize(query);
    if (tokens.isEmpty) return [];

    print("🔎 [Bloom] Checking remote filters for tokens: $tokens");

    _remoteFilters.forEach((peerId, filter) {
      bool hasAll = true;
      for (var t in tokens) {
        if (!filter.test(t)) {
          hasAll = false;
          break;
        }
      }

      if (hasAll) {
        print("✅ [Bloom] Candidate found: $peerId");
        candidates.add(peerId);
      }
    });

    return candidates;
  }

  // --- DOWNLOAD LOGIC ---

  void startDownload(String fileId, String name, int size, String peerId, Future<void> Function(String, int, int, List<int>) chunkSaver) {
    if (_activeDownloads.containsKey(fileId)) return;

    final totalChunks = (size / (512 * 1024)).ceil();
    final state = _DownloadState(fileId, name, size, totalChunks, peerId);
    _activeDownloads[fileId] = state;
  }

  // Called by Outbox when a chunk arrives
  void updateProgress(String fileId, int chunkIndex) {
    final state = _activeDownloads[fileId];
    if (state != null) {
      state.chunksReceived.add(chunkIndex);
      if (state.chunksReceived.length >= state.totalChunks) {
        state.isComplete = true;
      }
    }
  }

  List<TransferStatus> getTransfers() {
    return _activeDownloads.values.map((d) {
      return TransferStatus(
        fileId: d.id,
        name: d.name,
        peerId: d.peerId,
        progress: d.totalChunks == 0 ? 0 : d.chunksReceived.length / d.totalChunks,
        status: d.isComplete ? 'completed' : 'active'
      );
    }).toList();
  }
}

class _DownloadState {
  final String id;
  final String name;
  final int size;
  final int totalChunks;
  final String peerId;
  final Set<int> chunksReceived = {};
  bool isComplete = false;

  _DownloadState(this.id, this.name, this.size, this.totalChunks, this.peerId);
}