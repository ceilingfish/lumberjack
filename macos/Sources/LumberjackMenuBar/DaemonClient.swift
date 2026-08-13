import Foundation
import GRPC
import NIOCore
import NIOPosix

protocol DaemonConnection: Sendable {
    func health() async throws -> Lumberjack_V1_HealthResponse
    func listRepositories() async throws -> [Lumberjack_V1_Repository]
    func listWorktrees(repository: String) async throws -> [Lumberjack_V1_Worktree]
    func sync(repository: String) async throws
    func close() async
}

/// A connected handle to the Lumberjack daemon, over its Unix domain socket
/// (see SocketPath.swift). Uses `GRPCChannelPool`, which keeps a connection
/// warm and transparently reconnects — the piece that lets the menu bar app
/// recover when the daemon restarts without the app needing its own retry
/// loop around every call.
/// Only ever created, used, and closed from `AppState`'s `@MainActor` context;
/// `@unchecked` because gRPC's generated client types aren't marked `Sendable`
/// even though nothing here is actually shared across isolation domains.
final class DaemonClient: DaemonConnection, @unchecked Sendable {
    static let healthTimeout: TimeAmount = .seconds(2)

    private let group: EventLoopGroup
    private let channel: GRPCChannel
    let service: Lumberjack_V1_LumberjackServiceAsyncClient

    init(socketPath: String) throws {
        let group = MultiThreadedEventLoopGroup(numberOfThreads: 1)
        self.group = group
        self.channel = try GRPCChannelPool.with(
            target: .unixDomainSocket(socketPath),
            transportSecurity: .plaintext,
            eventLoopGroup: group
        )
        self.service = Lumberjack_V1_LumberjackServiceAsyncClient(channel: channel)
    }

    func close() async {
        try? await channel.close().get()
        try? await group.shutdownGracefully()
    }

    /// Health mirrors what the CLI does before any other call: a cheap probe
    /// for "is the daemon up at all", with a short deadline so a stopped
    /// daemon fails fast instead of hanging the UI.
    func health() async throws -> Lumberjack_V1_HealthResponse {
        try await service.health(
            Lumberjack_V1_HealthRequest(),
            callOptions: CallOptions(timeLimit: .timeout(Self.healthTimeout))
        )
    }

    func listRepositories() async throws -> [Lumberjack_V1_Repository] {
        try await service.listRepositories(Lumberjack_V1_ListRepositoriesRequest()).repositories
    }

    func listWorktrees(repository: String) async throws -> [Lumberjack_V1_Worktree] {
        var request = Lumberjack_V1_ListWorktreesRequest()
        request.repository = repository
        return try await service.listWorktrees(request).worktrees
    }

    /// Triggers a reconcile for one repository and waits for it to finish.
    /// `Sync` is a server-streaming RPC that emits progress updates; the menu
    /// bar only cares that it ran (the worktree poll picks up the results), so
    /// we drain the stream and discard the messages.
    func sync(repository: String) async throws {
        var request = Lumberjack_V1_SyncRequest()
        request.repository = repository
        for try await _ in service.sync(request) {}
    }
}
