import 'package:flutter/material.dart';
import 'package:tor_hidden_service/tor_hidden_service.dart';

// Logic
import 'logic/crypto_manager.dart';
import 'logic/server_node.dart';
import 'logic/outbox.dart';

// Screens
import 'screens/network_screen.dart';
import 'screens/chat_screen.dart';
import 'screens/files_screen.dart';
import 'screens/identity_screen.dart';

void main() async {
  WidgetsFlutterBinding.ensureInitialized();
  runApp(const AxonApp());
}

class AxonApp extends StatefulWidget {
  const AxonApp({super.key});

  // --- GLOBAL ACCESS POINT ---
  static late AxonClient client;

  @override
  State<AxonApp> createState() => AxonAppState();
}

class AxonAppState extends State<AxonApp> {
  final _tor = TorHiddenService();
  late CryptoManager _crypto;
  late AxonServer _server;

  String _status = "Initializing...";
  String _onion = "";
  bool _isReady = false;

  @override
  void initState() {
    super.initState();
    _bootSequence();
  }

  Future<void> _bootSequence() async {
    try {
      setState(() => _status = "Loading Keys...");
      _crypto = CryptoManager();
      await _crypto.init();

      setState(() => _status = "Starting Local Server...");
      _server = AxonServer(_crypto);
      await _server.start();

      // --- INITIALIZE GLOBAL CLIENT ---
      AxonApp.client = AxonClient(_tor, _crypto);

      setState(() => _status = "Bootstrapping Tor (Wait 30s)...");
      await _tor.start();

      final hostname = await _tor.getOnionHostname();

      setState(() {
        _status = "Online";
        _onion = hostname ?? "Error";
        _isReady = true;
      });
    } catch (e) {
      setState(() => _status = "Error: $e");
      print("CRITICAL BOOT ERROR: $e");
    }
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Axon Mesh',
      theme: ThemeData.dark().copyWith(
        scaffoldBackgroundColor: const Color(0xFF0b1120),
        primaryColor: const Color(0xFF06b6d4),
        appBarTheme: const AppBarTheme(color: Color(0xFF111827)),
        cardColor: const Color(0xFF1f2937),
      ),
      home: _isReady
        ? HomeScreen(status: _status, myOnion: _onion)
        : Scaffold(
            body: Center(
              child: Column(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  const CircularProgressIndicator(color: Color(0xFF06b6d4)),
                  const SizedBox(height: 20),
                  Text(_status, textAlign: TextAlign.center),
                ],
              ),
            ),
          ),
    );
  }
}

class HomeScreen extends StatefulWidget {
  final String status;
  final String myOnion;

  const HomeScreen({super.key, required this.status, required this.myOnion});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  int _selectedIndex = 0;

  static const List<Widget> _pages = <Widget>[
    NetworkScreen(),
    ChatScreen(),
    FilesScreen(),
    IdentityScreen(),
  ];

  void _onItemTapped(int index) {
    setState(() => _selectedIndex = index);
  }

  void _showAddPeerDialog() {
    final controller = TextEditingController();
    showDialog(
      context: context,
      builder: (context) => AlertDialog(
        backgroundColor: const Color(0xFF1f2937),
        title: const Text("Add Peer", style: TextStyle(color: Colors.white)),
        content: TextField(
          controller: controller,
          style: const TextStyle(color: Colors.white),
          decoration: const InputDecoration(hintText: "v3 Onion Address (.onion)", hintStyle: TextStyle(color: Colors.grey)),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context), child: const Text("Cancel")),
          ElevatedButton(
            style: ElevatedButton.styleFrom(backgroundColor: const Color(0xFF06b6d4)),
            onPressed: () async {
              Navigator.pop(context);
              ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text("Handshaking...")));
              await AxonApp.client.sendHandshake(controller.text);
            },
            child: const Text("Connect"),
          )
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('AXON // Mesh', style: TextStyle(fontWeight: FontWeight.bold)),
            Text("${widget.status} • ${widget.myOnion}", style: const TextStyle(fontSize: 10, color: Colors.grey, fontFamily: 'Courier')),
          ],
        ),
        actions: [
          if (_selectedIndex == 0)
            IconButton(icon: const Icon(Icons.add_circle_outline, color: Color(0xFF06b6d4)), onPressed: _showAddPeerDialog)
        ],
      ),
      body: _pages.elementAt(_selectedIndex),
      bottomNavigationBar: BottomNavigationBar(
        backgroundColor: const Color(0xFF111827),
        selectedItemColor: const Color(0xFF06b6d4),
        unselectedItemColor: Colors.grey,
        currentIndex: _selectedIndex,
        onTap: _onItemTapped,
        type: BottomNavigationBarType.fixed,
        items: const [
          BottomNavigationBarItem(icon: Icon(Icons.hub), label: 'Network'),
          BottomNavigationBarItem(icon: Icon(Icons.chat_bubble_outline), label: 'Comms'),
          BottomNavigationBarItem(icon: Icon(Icons.folder_open), label: 'Library'),
          BottomNavigationBarItem(icon: Icon(Icons.fingerprint), label: 'Identity'),
        ],
      ),
    );
  }
}