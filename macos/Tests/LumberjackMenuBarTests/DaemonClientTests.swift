import Foundation
import Testing

@testable import LumberjackMenuBar

struct DaemonClientTests {
    private func clientOnADeadSocket() throws -> DaemonClient {
        let path = FileManager.default.temporaryDirectory
            .appendingPathComponent("lumberjack-absent-\(UUID().uuidString).sock").path
        return try DaemonClient(socketPath: path)
    }

    @Test("dialling a socket nothing is listening on succeeds, because the pool connects lazily")
    func initialisingAgainstAnAbsentDaemonDoesNotThrow() async throws {
        let client = try clientOnADeadSocket()
        await client.close()
    }

    @Test("a health probe against an absent daemon fails rather than hanging")
    func healthFailsWithoutADaemon() async throws {
        let client = try clientOnADeadSocket()
        defer { Task { await client.close() } }

        await #expect(throws: (any Error).self) { try await client.health() }
    }

    @Test("every RPC surfaces a connection failure to its caller")
    func rpcsFailWithoutADaemon() async throws {
        let client = try clientOnADeadSocket()
        defer { Task { await client.close() } }

        let repositories = Task { try await client.listRepositories() }
        repositories.cancel()
        await #expect(throws: (any Error).self) { try await repositories.value }

        let worktrees = Task { try await client.listWorktrees(repository: "/tmp/repo") }
        worktrees.cancel()
        await #expect(throws: (any Error).self) { try await worktrees.value }

        let sync = Task { try await client.sync(repository: "/tmp/repo") }
        sync.cancel()
        await #expect(throws: (any Error).self) { try await sync.value }
    }
}
