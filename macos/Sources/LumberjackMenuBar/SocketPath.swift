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
        // Go's os.UserHomeDir() (used by pkg/client.DefaultSocketPath and the
        // daemon) reads $HOME on Unix. FileManager.homeDirectoryForCurrentUser
        // does not consult $HOME — it looks up the passwd-database home for
        // the current user — so it can disagree with the Go side whenever
        // $HOME is overridden (containers, some CI, `su`/`sudo -u`). Check
        // $HOME first to keep the two resolutions in agreement.
        if let home = ProcessInfo.processInfo.environment["HOME"], !home.isEmpty {
            return URL(fileURLWithPath: home).appendingPathComponent(".lumberjack/daemon.sock").path
        }
        let home = FileManager.default.homeDirectoryForCurrentUser
        return home.appendingPathComponent(".lumberjack/daemon.sock").path
    }
}
