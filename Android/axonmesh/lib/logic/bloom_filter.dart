// ====logic/bloom_filter.dart====
import 'dart:convert';
import 'dart:typed_data';

/// Standard configuration matching the Go implementation
const int FILTER_SIZE = 64 * 1024 * 8; // 524,288 bits
const int HASH_COUNT = 5;

class BloomFilter {
  final Uint8List bitSet;

  BloomFilter() : bitSet = Uint8List((FILTER_SIZE / 8).ceil());

  BloomFilter.fromBytes(List<int> bytes) : bitSet = Uint8List.fromList(bytes) {
    // Debug check: Is the filter empty?
    if (bitSet.every((b) => b == 0)) {
      print("⚠️ [Bloom] Warning: Initialized with empty bitset.");
    } else {
      print("✅ [Bloom] Initialized with ${bytes.length} bytes.");
    }
  }

  /// Add a string to the filter
  void add(String s) {
    s = s.trim().toLowerCase();
    _addTerm(s);
    final tokens = tokenize(s);
    for (var t in tokens) _addTerm(t);
  }

  /// Test if a specific term is in the set
  bool test(String s) {
    s = s.trim().toLowerCase();
    return _testTerm(s);
  }

  // --- INTERNAL HELPERS ---

  static List<String> tokenize(String s) {
    return s.toLowerCase()
        .split(RegExp(r'[^a-z0-9]'))
        .where((t) => t.isNotEmpty)
        .toList();
  }

  void _addTerm(String term) {
    final (h1, h2) = _hash(term);
    for (int i = 0; i < HASH_COUNT; i++) {
      final idx = _getIndex(h1, h2, i);
      final byteIdx = idx ~/ 8;
      final bitIdx = idx % 8;
      bitSet[byteIdx] |= (1 << bitIdx);
    }
  }

  bool _testTerm(String term) {
    final (h1, h2) = _hash(term);

    // Debug log for troubleshooting hashing mismatches
    // print("🔍 [Bloom] Testing '$term' | h1: $h1, h2: $h2");

    for (int i = 0; i < HASH_COUNT; i++) {
      final idx = _getIndex(h1, h2, i);
      final byteIdx = idx ~/ 8;
      final bitIdx = idx % 8;

      if ((bitSet[byteIdx] & (1 << bitIdx)) == 0) {
        // print("❌ [Bloom] Bit mismatch at index $idx");
        return false;
      }
    }
    // print("✅ [Bloom] Match found for '$term'");
    return true;
  }

  int _getIndex(BigInt h1, BigInt h2, int i) {
    // Go Logic: (h1 + uint64(i)*h2) % size
    // We use BigInt to ensure we don't lose precision on the addition before the modulo
    final bigI = BigInt.from(i);
    final size = BigInt.from(FILTER_SIZE);

    // We allow h1 + i*h2 to grow arbitrarily large (simulating wrapping is unnecessary for modulo power-of-2)
    // but strictly, we should wrap to 64-bit first if size wasn't power-of-2.
    // Since FILTER_SIZE (524288) is 2^19, the math holds safely.
    final res = (h1 + (bigI * h2)) % size;
    return res.toInt();
  }

  // FNV-1a 64-bit Hash (Strictly Unsigned)
  (BigInt, BigInt) _hash(String s) {
    final bytes = utf8.encode(s);
    var h1 = _fnv64a(bytes);
    final bytes2 = [...bytes, 1];
    var h2 = _fnv64a(bytes2);
    return (h1, h2);
  }

  BigInt _fnv64a(List<int> data) {
    // FNV constants (64-bit unsigned)
    // Offset: 14695981039346656037
    final offset = BigInt.parse("14695981039346656037");
    // Prime: 1099511628211
    final prime = BigInt.parse("1099511628211");
    // 2^64 for wrapping
    final mask64 = BigInt.parse("18446744073709551616");

    var hash = offset;

    for (var byte in data) {
      hash = hash ^ BigInt.from(byte);
      hash = (hash * prime) % mask64; // Force 64-bit wrapping
    }
    return hash;
  }
}