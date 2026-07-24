// swift-tools-version:6.0
import PackageDescription

let package = Package(
    name: "LumberjackMenuBar",
    platforms: [
        // v14+ because swift-testing's Testing.framework (used by the test
        // target) requires a macOS 14 minimum; a v13 deployment target
        // still builds but the test bundle fails to dlopen
        // Testing.framework at run time ("built for newer version 14.0").
        .macOS(.v14)
    ],
    dependencies: [
        .package(url: "https://github.com/grpc/grpc-swift.git", from: "1.21.0"),
        .package(url: "https://github.com/apple/swift-protobuf.git", from: "1.28.0"),
    ],
    targets: [
        .executableTarget(
            name: "LumberjackMenuBar",
            dependencies: [
                .product(name: "GRPC", package: "grpc-swift"),
                .product(name: "SwiftProtobuf", package: "swift-protobuf"),
            ]
        ),
        .testTarget(
            name: "LumberjackMenuBarTests",
            dependencies: ["LumberjackMenuBar"]
        ),
    ]
)
