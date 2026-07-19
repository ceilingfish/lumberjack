import Foundation

/// Resolves the daemon's Unix domain socket path, mirroring
/// `pkg/client.DefaultSocketPath` in the Go CLI: `LUMBERJACK_SOCKET_PATH` if
/// set, otherwise `~/.lumberjack/daemon.sock`. Client and server (and now this
/// app) all agree on this resolution without sharing code across languages.
enum SocketPath {
    static let envOverride = "LUMBERJACK_SOCKET_PATH"

    static func resolve() -> String {
        if let override = ProcessInfo.processInfo.environment[envOverride], !override.isEmpty {
            return override
        }
        let home = FileManager.default.homeDirectoryForCurrentUser
        return home.appendingPathComponent(".lumberjack/daemon.sock").path
    }
}
