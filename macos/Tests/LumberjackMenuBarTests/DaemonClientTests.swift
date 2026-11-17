import Foundation
import SwiftProtobuf
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

        await #expect(throws: (any Error).self) { try await client.health() }

        await client.close()
    }

    @Test("every RPC surfaces a connection failure to its caller")
    func rpcsFailWithoutADaemon() async throws {
        let client = try clientOnADeadSocket()

        let repositories = Task { try await client.listRepositories() }
        repositories.cancel()
        await #expect(throws: (any Error).self) { try await repositories.value }

        let worktrees = Task { try await client.listWorktrees(repository: "/tmp/repo") }
        worktrees.cancel()
        await #expect(throws: (any Error).self) { try await worktrees.value }

        let sync = Task { try await client.sync(repository: "/tmp/repo") }
        sync.cancel()
        await #expect(throws: (any Error).self) { try await sync.value }

        let viewed = Task {
            try await client.markViewed(repository: "/tmp/repo", authorship: .mine, seenThrough: Google_Protobuf_Timestamp())
        }
        viewed.cancel()
        await #expect(throws: (any Error).self) { try await viewed.value }

        let viewedWithoutTime = Task {
            try await client.markViewed(repository: "/tmp/repo", authorship: .theirs, seenThrough: nil)
        }
        viewedWithoutTime.cancel()
        await #expect(throws: (any Error).self) { try await viewedWithoutTime.value }

        await client.close()
    }
}
