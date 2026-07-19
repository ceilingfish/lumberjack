import Foundation
import UserNotifications

/// Posts native macOS notifications for worktree clone/delete events. A thin
/// wrapper so the rest of the app depends on an intent ("a worktree appeared")
/// rather than the UserNotifications API directly.
struct Notifier {
    static func requestAuthorization() {
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) { _, _ in }
    }

    static func worktreeCreated(repository: String, branch: String) {
        post(
            title: "Worktree cloned",
            body: "\(repository): \(branch)"
        )
    }

    static func worktreeDeleted(repository: String, branch: String) {
        post(
            title: "Worktree deleted",
            body: "\(repository): \(branch)"
        )
    }

    private static func post(title: String, body: String) {
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        let request = UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil)
        UNUserNotificationCenter.current().add(request)
    }
}
