import 'package:flutter/material.dart';
import '../main.dart'; // Access AxonApp

class FilesScreen extends StatefulWidget {
  const FilesScreen({super.key});

  @override
  State<FilesScreen> createState() => _FilesScreenState();
}

class _FilesScreenState extends State<FilesScreen> {
  final TextEditingController _searchCtrl = TextEditingController();
  List<Map<String, dynamic>> _results = [];
  bool _searching = false;

  Future<void> _doSearch() async {
    if (_searchCtrl.text.isEmpty) return;
    setState(() { _searching = true; _results = []; });

    // FIX: Use AxonApp.client (not AxonAppState)
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
      SnackBar(content: Text("Downloading ${file['name']}..."))
    );

    // FIX: Use AxonApp.client
    AxonApp.client.downloadFile(
      file['owner'],
      file['id'],
      file['name'],
      file['size']
    ).then((_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text("Download Complete!"))
        );
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
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
                    hintText: "Search mesh...",
                    hintStyle: TextStyle(color: Colors.grey),
                    prefixIcon: Icon(Icons.search, color: Colors.grey),
                    border: OutlineInputBorder(),
                    isDense: true,
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
        Expanded(
          child: _results.isEmpty && !_searching
            ? const Center(child: Text("Search for files to download", style: TextStyle(color: Colors.grey)))
            : ListView.builder(
                itemCount: _results.length,
                itemBuilder: (context, index) {
                  final file = _results[index];
                  final sizeMb = (file['size'] / 1024 / 1024).toStringAsFixed(2);
                  return ListTile(
                    leading: const Icon(Icons.insert_drive_file, color: Colors.blue),
                    title: Text(file['name'], style: const TextStyle(color: Colors.white)),
                    subtitle: Text("${sizeMb} MB • Owner: ${file['owner'].substring(0,12)}...",
                      style: const TextStyle(color: Colors.grey, fontSize: 10)),
                    trailing: IconButton(
                      icon: const Icon(Icons.download, color: Color(0xFF06b6d4)),
                      onPressed: () => _download(file),
                    ),
                  );
                },
              ),
        ),
      ],
    );
  }
}