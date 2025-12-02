import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:tor_hidden_service/tor_hidden_service.dart';

class IdentityScreen extends StatefulWidget {
  const IdentityScreen({super.key});

  @override
  State<IdentityScreen> createState() => _IdentityScreenState();
}

class _IdentityScreenState extends State<IdentityScreen> {
  String _onion = "Loading...";
  final TextEditingController _nickCtrl = TextEditingController();

  @override
  void initState() {
    super.initState();
    _loadIdentity();
  }

  Future<void> _loadIdentity() async {
    // Get Onion
    final onion = await TorHiddenService().getOnionHostname();

    // Get Nickname
    final prefs = await SharedPreferences.getInstance();
    final nick = prefs.getString('nickname') ?? 'Anonymous';

    if (mounted) {
      setState(() {
        _onion = onion ?? "Not ready";
        _nickCtrl.text = nick;
      });
    }
  }

  Future<void> _save() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString('nickname', _nickCtrl.text);
    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text("Identity Saved"))
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(24.0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text("MY IDENTITY", style: TextStyle(color: Color(0xFF06b6d4), fontWeight: FontWeight.bold)),
          const SizedBox(height: 20),

          const Text("Onion Address", style: TextStyle(color: Colors.grey, fontSize: 12)),
          const SizedBox(height: 5),
          InkWell(
            onTap: () {
              Clipboard.setData(ClipboardData(text: _onion));
              ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text("Copied!")));
            },
            child: Container(
              width: double.infinity,
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: const Color(0xFF1f2937),
                borderRadius: BorderRadius.circular(4),
                border: Border.all(color: Colors.grey.withOpacity(0.2))
              ),
              child: Text(_onion, style: const TextStyle(fontFamily: 'Courier', color: Colors.white)),
            ),
          ),

          const SizedBox(height: 30),

          const Text("Display Name", style: TextStyle(color: Colors.grey, fontSize: 12)),
          const SizedBox(height: 5),
          Row(
            children: [
              Expanded(
                child: TextField(
                  controller: _nickCtrl,
                  decoration: const InputDecoration(
                    filled: true,
                    fillColor: Color(0xFF1f2937),
                    border: OutlineInputBorder(),
                  ),
                ),
              ),
              const SizedBox(width: 10),
              ElevatedButton(
                onPressed: _save,
                style: ElevatedButton.styleFrom(backgroundColor: const Color(0xFF06b6d4)),
                child: const Text("Save"),
              )
            ],
          )
        ],
      ),
    );
  }
}