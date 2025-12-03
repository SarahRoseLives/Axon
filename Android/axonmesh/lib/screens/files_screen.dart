// ====screens/files_screen.dart====
import 'dart:async';
import 'package:flutter/material.dart';
import '../main.dart'; // Access AxonApp
import '../logic/file_manager.dart'; // Access FileManager logic

class FilesScreen extends StatefulWidget {
  const FilesScreen({super.key});

  @override
  State<FilesScreen> createState() => _FilesScreenState();
}

class _FilesScreenState extends State<FilesScreen> {
  final TextEditingController _searchCtrl = TextEditingController();
  List<Map<String, dynamic>> _results = [];
  List<TransferStatus> _transfers = [];
  bool _searching = false;
  Timer? _pollTimer;

  @override
  void initState() {
    super.initState();
    // Poll the local Dart FileManager for progress every second
    _pollTimer = Timer.periodic(const Duration(seconds: 1), (_) => _pollTransfers());
  }

  @override
  void dispose() {
    _pollTimer?.cancel();
    super.dispose();
  }

  Future<void> _pollTransfers() async {
    // Direct call to local singleton (No HTTP/JSON needed for local status)
    final status = FileManager().getTransfers();
    if (mounted) {
      setState(() => _transfers = status);
    }
  }

  Future<void> _doSearch() async {
    if (_searchCtrl.text.isEmpty) return;
    setState(() { _searching = true; _results = []; });

    // 1. Logic: Client checks Bloom filters, then queries specific peers via Tor
    final res = await AxonApp.client.searchRemoteFiles(_searchCtrl.text);

    if (mounted) {
      setState(() {
        _results = res;
        _searching = false;
      });
    }
  }

  Future<void> _download(Map<String, dynamic> file) async {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text("Requesting ${file['name']} from ${file['owner'].substring(0,16)}..."))
    );

    // 2. Logic: Client orchestrates the download using FileManager
    await AxonApp.client.downloadFile(
      file['owner'],
      file['id'],
      file['name'],
      file['size']
    );
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        // --- SEARCH BAR ---
        Container(
          padding: const EdgeInsets.all(16),
          color: const Color(0xFF1f2937),
          child: Row(
            children: [
              Expanded(
                child: TextField(
                  controller: _searchCtrl,
                  style: const TextStyle(color: Colors.white),
                  decoration: const InputDecoration(
                    hintText: "Search via Bloom Filters...",
                    hintStyle: TextStyle(color: Colors.grey),
                    prefixIcon: Icon(Icons.search, color: Colors.grey),
                    border: OutlineInputBorder(),
                    isDense: true,
                    filled: true,
                    fillColor: Color(0xFF111827),
                  ),
                  onSubmitted: (_) => _doSearch(),
                ),
              ),
              const SizedBox(width: 10),
              IconButton(
                icon: _searching
                  ? const SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2))
                  : const Icon(Icons.arrow_forward, color: Color(0xFF06b6d4)),
                onPressed: _searching ? null : _doSearch,
              )
            ],
          ),
        ),

        // --- ACTIVE TRANSFERS AREA ---
        if (_transfers.isNotEmpty)
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            color: const Color(0xFF111827),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text("ACTIVE TRANSFERS", style: TextStyle(color: Color(0xFF06b6d4), fontSize: 10, fontWeight: FontWeight.bold)),
                const SizedBox(height: 5),
                ..._transfers.map((t) {
                  final percent = (t.progress * 100).toStringAsFixed(0);
                  return Padding(
                    padding: const EdgeInsets.symmetric(vertical: 4),
                    child: Row(
                      children: [
                        Icon(t.status == 'completed' ? Icons.check_circle : Icons.download,
                             size: 14,
                             color: t.status == 'completed' ? Colors.green : Colors.grey),
                        const SizedBox(width: 8),
                        Expanded(child: Text(t.name, style: const TextStyle(color: Colors.white, fontSize: 12), overflow: TextOverflow.ellipsis)),
                        const SizedBox(width: 8),
                        Text("$percent%", style: const TextStyle(color: Color(0xFF06b6d4), fontSize: 12, fontWeight: FontWeight.bold)),
                      ],
                    ),
                  );
                }).toList(),
              ],
            ),
          ),

        // --- RESULTS LIST ---
        Expanded(
          child: _results.isEmpty && !_searching
            ? const Center(
                child: Column(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    Icon(Icons.folder_open, size: 48, color: Colors.grey),
                    SizedBox(height: 10),
                    Text("Bloom Filter Search Ready", style: TextStyle(color: Colors.grey)),
                  ],
                ),
              )
            : ListView.builder(
                itemCount: _results.length,
                itemBuilder: (context, index) {
                  final file = _results[index];
                  final sizeMb = (file['size'] / 1024 / 1024).toStringAsFixed(2);
                  final ownerShort = file['owner'].toString().substring(0, 16);

                  return Card(
                    margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
                    color: const Color(0xFF1f2937),
                    child: ListTile(
                      leading: const Icon(Icons.insert_drive_file, color: Colors.blueGrey),
                      title: Text(file['name'], style: const TextStyle(color: Colors.white)),
                      subtitle: Row(
                        children: [
                          Text("$sizeMb MB", style: const TextStyle(color: Color(0xFF06b6d4), fontSize: 11)),
                          const SizedBox(width: 10),
                          const Icon(Icons.person, size: 12, color: Colors.grey),
                          const SizedBox(width: 4),
                          Expanded(
                            child: Text(
                              "$ownerShort...",
                              style: const TextStyle(color: Colors.grey, fontSize: 11, fontFamily: 'monospace'),
                              overflow: TextOverflow.ellipsis
                            ),
                          ),
                        ],
                      ),
                      trailing: IconButton(
                        icon: const Icon(Icons.download_rounded, color: Colors.white),
                        onPressed: () => _download(file),
                      ),
                    ),
                  );
                },
              ),
        ),
      ],
    );
  }
}