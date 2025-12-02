import 'dart:convert';
import 'package:cryptography/cryptography.dart';
import 'package:shared_preferences/shared_preferences.dart';

class CryptoManager {
  final _ed25519 = Ed25519();
  final _x25519 = X25519();

  // Go uses standard AES-GCM (12 byte nonce, 16 byte tag)
  final _aesGcm = AesGcm.with256bits(nonceLength: 12);

  SimpleKeyPair? _identityKey;
  SimpleKeyPair? _chatKey;

  Future<void> init() async {
    final prefs = await SharedPreferences.getInstance();

    if (prefs.containsKey('id_priv')) {
      final bytes = base64Decode(prefs.getString('id_priv')!);
      _identityKey = await _ed25519.newKeyPairFromSeed(bytes);
    } else {
      _identityKey = await _ed25519.newKeyPair();
      final seed = await _identityKey!.extractPrivateKeyBytes();
      await prefs.setString('id_priv', base64Encode(seed));
    }

    if (prefs.containsKey('chat_priv')) {
      final bytes = base64Decode(prefs.getString('chat_priv')!);
      _chatKey = await _x25519.newKeyPairFromSeed(bytes);
    } else {
      _chatKey = await _x25519.newKeyPair();
      final seed = await _chatKey!.extractPrivateKeyBytes();
      await prefs.setString('chat_priv', base64Encode(seed));
    }
  }

  Future<String> getChatPublicKey() async {
    final pub = await _chatKey!.extractPublicKey();
    return hex.encode(pub.bytes);
  }

  Future<String> getIdentityPublicKey() async {
    final pub = await _identityKey!.extractPublicKey();
    return hex.encode(pub.bytes);
  }

  Future<String> sign(String data) async {
    final sig = await _ed25519.sign(
      utf8.encode(data),
      keyPair: _identityKey!,
    );
    return hex.encode(sig.bytes);
  }

  // --- ENCRYPTION (FIXED FOR GO COMPATIBILITY) ---
  Future<Map<String, String>> encrypt(String peerPubKeyHex, String plaintext) async {
    final peerBytes = hex.decode(peerPubKeyHex);
    final peerKey = SimplePublicKey(peerBytes, type: KeyPairType.x25519);

    final sharedSecret = await _x25519.sharedSecretKey(
      keyPair: _chatKey!,
      remotePublicKey: peerKey,
    );

    final secretBox = await _aesGcm.encrypt(
      utf8.encode(plaintext),
      secretKey: sharedSecret,
    );

    // CRITICAL FIX: Concatenate CipherText + MAC (Tag)
    // Go's gcm.Seal appends the tag. We must do the same manually.
    final combinedBytes = [...secretBox.cipherText, ...secretBox.mac.bytes];

    return {
      'ciphertext': hex.encode(combinedBytes),
      'nonce': hex.encode(secretBox.nonce),
    };
  }

  Future<String> decrypt(String peerPubKeyHex, String ciphertextHex, String nonceHex) async {
    final peerBytes = hex.decode(peerPubKeyHex);
    final peerKey = SimplePublicKey(peerBytes, type: KeyPairType.x25519);

    final sharedSecret = await _x25519.sharedSecretKey(
      keyPair: _chatKey!,
      remotePublicKey: peerKey,
    );

    final allBytes = hex.decode(ciphertextHex);

    // Go sends [Ciphertext + MAC]. We split them here.
    if (allBytes.length < 16) throw Exception("Invalid Ciphertext Length");

    final macBytes = allBytes.sublist(allBytes.length - 16);
    final cipherTextBytes = allBytes.sublist(0, allBytes.length - 16);

    final secretBox = SecretBox(
      cipherTextBytes,
      nonce: hex.decode(nonceHex),
      mac: Mac(macBytes),
    );

    final clearText = await _aesGcm.decrypt(
      secretBox,
      secretKey: sharedSecret,
    );

    return utf8.decode(clearText);
  }
}

class hex {
  static String encode(List<int> bytes) =>
      bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join();

  static List<int> decode(String hex) {
    List<int> bytes = [];
    for (int i = 0; i < hex.length; i += 2) {
      bytes.add(int.parse(hex.substring(i, i + 2), radix: 16));
    }
    return bytes;
  }
}