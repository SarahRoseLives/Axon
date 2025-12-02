import 'package:sqflite/sqflite.dart';
import 'package:path/path.dart';
import 'package:path_provider/path_provider.dart';

class AxonDatabase {
  static Database? _db;

  static Future<Database> get db async {
    if (_db != null) return _db!;
    _db = await _init();
    return _db!;
  }

  static Future<Database> _init() async {
    final dir = await getApplicationDocumentsDirectory();
    final path = join(dir.path, 'axon.db');

    return await openDatabase(
      path,
      version: 1,
      onCreate: (db, version) async {
        // 1. Peers Table
        await db.execute('''
          CREATE TABLE peers (
            onion_address TEXT PRIMARY KEY,
            nickname TEXT,
            public_key TEXT,
            trust_level TEXT,
            last_seen TEXT,
            is_blocked INTEGER DEFAULT 0
          )
        ''');

        // 2. Messages Table
        await db.execute('''
          CREATE TABLE messages (
            id TEXT PRIMARY KEY,
            peer_id TEXT,
            direction TEXT, -- 'in', 'out', 'feed'
            content TEXT,
            status TEXT, -- 'pending', 'received'
            timestamp TEXT
          )
        ''');

        // 3. Files Table
        await db.execute('''
          CREATE TABLE my_files (
            id TEXT PRIMARY KEY,
            name TEXT,
            size INTEGER,
            path TEXT,
            hash TEXT
          )
        ''');
      },
    );
  }
}