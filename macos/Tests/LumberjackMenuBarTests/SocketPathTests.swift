import Testing

@testable import LumberjackMenuBar

struct SocketPathTests {
    @Test("the environment override wins outright")
    func overrideWins() {
        let resolved = SocketPath.resolve(environment: [
            SocketPath.envOverride: "/var/run/lumberjack.sock",
            "HOME": "/Users/somebody",
        ])

        #expect(resolved == "/var/run/lumberjack.sock")
    }

    @Test("an empty override is ignored in favour of HOME")
    func emptyOverrideFallsBackToHome() {
        let resolved = SocketPath.resolve(environment: [
            SocketPath.envOverride: "",
            "HOME": "/Users/somebody",
        ])

        #expect(resolved == "/Users/somebody/.lumberjack/daemon.sock")
    }

    @Test("HOME is what resolution follows, matching Go's os.UserHomeDir")
    func homeIsUsedWhenThereIsNoOverride() {
        #expect(SocketPath.resolve(environment: ["HOME": "/tmp/home"]) == "/tmp/home/.lumberjack/daemon.sock")
        #expect(SocketPath.resolve(environment: ["HOME": ""]).hasSuffix("/.lumberjack/daemon.sock"))
    }

    @Test("with nothing in the environment it falls back to the current user's home")
    func emptyEnvironmentFallsBackToTheUserDirectory() {
        let resolved = SocketPath.resolve(environment: [:])

        #expect(resolved.hasSuffix("/.lumberjack/daemon.sock"))
        #expect(resolved.hasPrefix("/"))
    }
}
