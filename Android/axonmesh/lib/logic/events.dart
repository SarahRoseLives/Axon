// ====logic/events.dart====
import 'dart:async';

class AxonEvents {
  // A broadcast stream allows multiple screens (Chat List AND Chat Detail) to listen simultaneously.
  static final StreamController<void> _messageController = StreamController.broadcast();

  // The stream the UI listens to
  static Stream<void> get onMessage => _messageController.stream;

  // Called by ServerNode (incoming) and Outbox (outgoing success)
  static void triggerMessageUpdate() {
    _messageController.add(null);
  }
}