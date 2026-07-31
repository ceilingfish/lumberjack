#!/usr/bin/env pwsh

BeforeAll {
    . (Join-Path $PSScriptRoot 'coverage-gate.ps1')
}

Describe 'Test-GlobMatch' {
    It 'matches <pattern> against <path> as <expected>' -ForEach @(
        @{ Pattern = 'main.go'; Path = 'main.go'; Expected = $true }
        @{ Pattern = 'main.go'; Path = 'cmd/main.go'; Expected = $true }
        @{ Pattern = '/main.go'; Path = 'main.go'; Expected = $true }
        @{ Pattern = '/main.go'; Path = 'cmd/main.go'; Expected = $false }
        @{ Pattern = '/*.go'; Path = 'main.go'; Expected = $true }
        @{ Pattern = '/*.go'; Path = 'cmd/main.go'; Expected = $false }
        @{ Pattern = '/**/*.pb.go'; Path = 'pkg/client/v1/lumberjack.pb.go'; Expected = $true }
        @{ Pattern = 'MenuBarView.swift'; Path = 'macos/Sources/LumberjackMenuBar/MenuBarView.swift'; Expected = $true }
        @{ Pattern = 'internal/database/migrations/embed.go'; Path = 'internal/database/migrations/embed.go'; Expected = $true }
        @{ Pattern = 'internal/database/migrations/embed.go'; Path = 'internal/database/embed.go'; Expected = $false }
        @{ Pattern = '**/*.pb.go'; Path = 'pkg/client/lumberjack/v1/lumberjack.pb.go'; Expected = $true }
        @{ Pattern = '**/*.pb.go'; Path = 'lumberjack.pb.go'; Expected = $true }
        @{ Pattern = '**/*.pb.go'; Path = 'pkg/client/lumberjack/v1/lumberjack_grpc.pb.go'; Expected = $true }
        @{ Pattern = '**/*.grpc.swift'; Path = 'macos/Sources/LumberjackMenuBar/Generated/lumberjack/v1/lumberjack.grpc.swift'; Expected = $true }
        @{ Pattern = '**/*.pb.go'; Path = 'internal/present/color.go'; Expected = $false }
    ) {
        Test-GlobMatch -Pattern $Pattern -Path $Path | Should -Be $Expected
    }
}

Describe 'Read-Exclusions' {
    It 'keeps globs and drops blank lines and comments' {
        $patterns = Read-Exclusions -Lines @(
            ''
            '# a comment'
            'main.go'
            '  **/*.pb.go  '
        )
        $patterns | Should -Be @('main.go', '**/*.pb.go')
    }
}

Describe 'Read-Lcov' {
    It 'counts instrumented and hit lines per file' {
        $files = Read-Lcov -Lines @(
            'SF:internal/present/color.go'
            'DA:3,1'
            'DA:4,0'
            'DA:5,12'
            'LF:3'
            'LH:2'
            'end_of_record'
            'SF:macos/Sources/LumberjackMenuBar/AppState.swift'
            'FN:10,foo'
            'DA:10,0'
            'end_of_record'
        )

        $files.Count | Should -Be 2
        $files[0].File | Should -Be 'internal/present/color.go'
        $files[0].Lines | Should -Be 3
        $files[0].Covered | Should -Be 2
        $files[1].Lines | Should -Be 1
        $files[1].Covered | Should -Be 0
    }

    It 'merges repeated sections for one file' {
        $files = Read-Lcov -Lines @('SF:a.go', 'DA:1,1', 'end_of_record', 'SF:a.go', 'DA:2,0', 'end_of_record')
        $files.Count | Should -Be 1
        $files[0].Lines | Should -Be 2
        $files[0].Covered | Should -Be 1
    }

    It 'rejects a DA record with no preceding SF record' {
        { Read-Lcov -Lines @('DA:1,1') } | Should -Throw '*DA record before any SF record*'
    }

    It 'rejects a malformed hit count' {
        { Read-Lcov -Lines @('SF:a.go', 'DA:1,not-a-number') } | Should -Throw '*bad hit count*'
    }
}

Describe 'Invoke-Gate' {
    BeforeAll {
        $script:files = Read-Lcov -Lines @(
            'SF:main.go', 'DA:7,0', 'DA:8,0', 'DA:9,0'
            'SF:good/good.go', 'DA:1,1', 'DA:2,3', 'DA:3,1'
            'SF:bad/bad.go', 'DA:1,1', 'DA:2,0', 'DA:3,0', 'DA:4,0', 'DA:5,0'
            'SF:good/good.pb.go', 'DA:1,0', 'DA:2,0'
            'SF:macos/Sources/LumberjackMenuBar/AppState.swift', 'DA:1,1', 'DA:2,1', 'DA:3,0', 'DA:4,0'
        )
        $script:packages = @(
            [pscustomobject]@{ Dir = '.'; GoFiles = @('main.go'); TestFileCount = 0 }
            [pscustomobject]@{ Dir = 'good'; GoFiles = @('good.go', 'good.pb.go'); TestFileCount = 1 }
            [pscustomobject]@{ Dir = 'bad'; GoFiles = @('bad.go'); TestFileCount = 1 }
            [pscustomobject]@{ Dir = 'untested'; GoFiles = @('predicate.go'); TestFileCount = 0 }
        )
        $script:exclusions = @('/main.go', '**/*.pb.go')

        $results = Invoke-Gate -Packages $script:packages -Files $script:files `
            -ExclusionPatterns $script:exclusions -Floor 80
        $script:byDir = @{}
        foreach ($result in $results) { $script:byDir[$result.Dir] = $result }
    }

    It 'excuses a package whose every file is excluded' {
        $byDir['.'].Excluded | Should -BeTrue
        $byDir['.'].Pass | Should -BeTrue
    }

    It 'passes a package above the floor, ignoring its excluded files' {
        $byDir['good'].Percent | Should -Be 100
        $byDir['good'].Pass | Should -BeTrue
    }

    It 'fails a package below the floor' {
        $byDir['bad'].Percent | Should -Be 20
        $byDir['bad'].Pass | Should -BeFalse
    }

    It 'fails a package with source but no test files' {
        $byDir['untested'].NoTests | Should -BeTrue
        $byDir['untested'].Pass | Should -BeFalse
    }

    It 'gates the Swift source directory as a package of its own' {
        $swift = $byDir['macos/Sources/LumberjackMenuBar']
        $swift | Should -Not -BeNullOrEmpty
        $swift.Percent | Should -Be 50
        $swift.Pass | Should -BeFalse
    }

    It 'reports packages lowest coverage first' {
        $ordered = Invoke-Gate -Packages $script:packages -Files $script:files `
            -ExclusionPatterns $script:exclusions -Floor 80
        $percents = @($ordered | ForEach-Object { $_.Percent })
        $sorted = @($percents | Sort-Object)
        $percents | Should -Be $sorted
    }

    It 'fails a package with no tests even at a floor of zero' {
        $results = Invoke-Gate -Packages $script:packages -Files $script:files `
            -ExclusionPatterns $script:exclusions -Floor 0
        ($results | Where-Object { $_.Dir -eq 'untested' }).Pass | Should -BeFalse
        ($results | Where-Object { $_.Dir -eq 'bad' }).Pass | Should -BeTrue
    }
}

Describe 'Get-Half' {
    It 'names <dir> as <expected>' -ForEach @(
        @{ Dir = 'macos/Sources/LumberjackMenuBar'; Expected = 'Swift' }
        @{ Dir = 'macos'; Expected = 'Swift' }
        @{ Dir = 'internal/present'; Expected = 'Go' }
        @{ Dir = '.'; Expected = 'Go' }
    ) {
        Get-Half -Dir $Dir | Should -Be $Expected
    }
}
